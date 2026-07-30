package alicloud

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	alicloudOpenapiClient "github.com/alibabacloud-go/darabonba-openapi/v2/client"
)

var (
	_ resource.Resource                = &kvstoreIndividualShardBandwidthResource{}
	_ resource.ResourceWithConfigure   = &kvstoreIndividualShardBandwidthResource{}
	_ resource.ResourceWithImportState = &kvstoreIndividualShardBandwidthResource{}
)

func NewKvstoreIndividualShardBandwidthResource() resource.Resource {
	return &kvstoreIndividualShardBandwidthResource{}
}

type kvstoreIndividualShardBandwidthResource struct {
	client *alicloudOpenapiClient.Client
}

type kvstoreIndividualShardBandwidthModel struct {
	Id         types.String `tfsdk:"id"`
	InstanceId types.String `tfsdk:"instance_id"`
	ShardId    types.String `tfsdk:"shard_id"`
	Bandwidth  types.Int64  `tfsdk:"bandwidth"`
}

func (r *kvstoreIndividualShardBandwidthResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kvstore_individual_shard_bandwidth"
}

func (r *kvstoreIndividualShardBandwidthResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages additional individual shard bandwidth for an Alibaba Cloud Redis (R-Kvstore) instance. " +
			"This purchases permanent additional bandwidth for a specific shard (node). " +
			"Use `DescribeRoleZoneInfo` or `DescribeLogicInstanceTopology` to list available shard IDs.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The resource ID. Format: `instance_id:shard_id`.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"instance_id": schema.StringAttribute{
				Description: "The ID of the Redis instance.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"shard_id": schema.StringAttribute{
				Description: "The shard (node) ID in InsName format (e.g. `r-xxx-db-0`). " +
					"Use `DescribeRoleZoneInfo` or `DescribeLogicInstanceTopology` to list available shard IDs. " +
					"Must match the pattern `r-<instance_id>-db-<N>` (with hyphens around `db`).",
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^r-[a-z0-9]+-db-\d+$`),
						"shard_id must match the pattern r-<instance_id>-db-<N> (e.g. r-xxxxx-db-0). Ensure hyphens around 'db'.",
					),
				},
			},
			"bandwidth": schema.Int64Attribute{
				Description: "Total desired bandwidth in MB/s for the shard (default + additional). " +
					"The provider reads DefaultBandWidth from the API and calculates the additional " +
					"bandwidth to purchase. Must be >= DefaultBandWidth. " +
					"Example: if DefaultBandWidth is 48 and you set 50, the provider purchases 2 MB/s additional.",
				Required: true,
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
		},
	}
}

func (r *kvstoreIndividualShardBandwidthResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(alicloudClients).kvstoreRawClient
}

// --- CRUD ---

func (r *kvstoreIndividualShardBandwidthResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan *kvstoreIndividualShardBandwidthModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := plan.InstanceId.ValueString()
	shardId := plan.ShardId.ValueString()
	desiredBw := plan.Bandwidth.ValueInt64()

	// Read DefaultBandWidth from API to calculate additional bandwidth.
	_, defaultBw, _, err := kvstoreReadNodeBandwidth(r.client, instanceId, shardId)
	if err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to read DefaultBandWidth for shard.",
			fmt.Sprintf("instance: %s, shard: %s, error: %s", instanceId, shardId, err.Error()),
		)
		return
	}

	if desiredBw < defaultBw {
		resp.Diagnostics.AddError(
			"[VALIDATION ERROR] Bandwidth cannot be less than DefaultBandWidth.",
			fmt.Sprintf("Requested %d MB/s but DefaultBandWidth is %d MB/s. Set bandwidth >= %d.",
				desiredBw, defaultBw, defaultBw),
		)
		return
	}

	additionalBw := desiredBw - defaultBw

	if additionalBw == 0 {
		resp.Diagnostics.AddError(
			"[VALIDATION ERROR] Bandwidth equals DefaultBandWidth.",
			fmt.Sprintf("Requested %d MB/s equals DefaultBandWidth %d MB/s — no additional bandwidth to purchase. Set bandwidth > %d.",
				desiredBw, defaultBw, defaultBw),
		)
		return
	}

	if err := r.setBandwidth(instanceId, shardId, additionalBw); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to set Redis individual shard bandwidth.",
			err.Error(),
		)
		return
	}

	if err := r.verifyBandwidth(instanceId, shardId, desiredBw, defaultBw); err != nil {
		resp.Diagnostics.AddError(
			"[VERIFY ERROR] Apply succeeded but read-back verification failed.",
			err.Error(),
		)
		return
	}

	state := &kvstoreIndividualShardBandwidthModel{
		Id:         types.StringValue(makeShardId(instanceId, shardId)),
		InstanceId: plan.InstanceId,
		ShardId:    plan.ShardId,
		Bandwidth:  plan.Bandwidth,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstoreIndividualShardBandwidthResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state *kvstoreIndividualShardBandwidthModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := state.InstanceId.ValueString()
	shardId := state.ShardId.ValueString()

	currentBw, _, _, err := kvstoreReadNodeBandwidth(r.client, instanceId, shardId)
	if err != nil {
		// Instance or shard may be gone — remove from state.
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") ||
			strings.Contains(errStr, "notfound") ||
			strings.Contains(errStr, "invalidinstance") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to read Redis individual shard bandwidth.",
			err.Error(),
		)
		return
	}

	state.Id = types.StringValue(makeShardId(instanceId, shardId))

	// During import, bandwidth is null — compute total from API.
	// Otherwise keep the plan/state value (do NOT override — prevents diff loops).
	if state.Bandwidth.IsNull() {
		state.Bandwidth = types.Int64Value(currentBw)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstoreIndividualShardBandwidthResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan *kvstoreIndividualShardBandwidthModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := plan.InstanceId.ValueString()
	shardId := plan.ShardId.ValueString()
	desiredBw := plan.Bandwidth.ValueInt64()

	// Read DefaultBandWidth from API to calculate additional bandwidth.
	_, defaultBw, _, err := kvstoreReadNodeBandwidth(r.client, instanceId, shardId)
	if err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to read DefaultBandWidth for shard.",
			fmt.Sprintf("instance: %s, shard: %s, error: %s", instanceId, shardId, err.Error()),
		)
		return
	}

	if desiredBw < defaultBw {
		resp.Diagnostics.AddError(
			"[VALIDATION ERROR] Bandwidth cannot be less than DefaultBandWidth.",
			fmt.Sprintf("Requested %d MB/s but DefaultBandWidth is %d MB/s. Set bandwidth >= %d.",
				desiredBw, defaultBw, defaultBw),
		)
		return
	}

	additionalBw := desiredBw - defaultBw

	if additionalBw == 0 {
		resp.Diagnostics.AddError(
			"[VALIDATION ERROR] Bandwidth equals DefaultBandWidth.",
			fmt.Sprintf("Requested %d MB/s equals DefaultBandWidth %d MB/s — no additional bandwidth to purchase. Set bandwidth > %d.",
				desiredBw, defaultBw, defaultBw),
		)
		return
	}

	if err := r.setBandwidth(instanceId, shardId, additionalBw); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to update Redis individual shard bandwidth.",
			err.Error(),
		)
		return
	}

	if err := r.verifyBandwidth(instanceId, shardId, desiredBw, defaultBw); err != nil {
		resp.Diagnostics.AddError(
			"[VERIFY ERROR] Update succeeded but read-back verification failed.",
			err.Error(),
		)
		return
	}

	state := &kvstoreIndividualShardBandwidthModel{
		Id:         types.StringValue(makeShardId(instanceId, shardId)),
		InstanceId: plan.InstanceId,
		ShardId:    plan.ShardId,
		Bandwidth:  plan.Bandwidth,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstoreIndividualShardBandwidthResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state *kvstoreIndividualShardBandwidthModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := state.InstanceId.ValueString()
	shardId := state.ShardId.ValueString()

	// Reset shard bandwidth to 0 (default). Uses EnableAdditionalBandwidth with
	// Bandwidth=0 — ModifyIntranetAttribute returns ModifyBandWidth.NotSupport
	// for many instance types.
	if err := r.setBandwidth(instanceId, shardId, 0); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to reset Redis individual shard bandwidth.",
			err.Error(),
		)
		return
	}
}

func (r *kvstoreIndividualShardBandwidthResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Format: instance_id:shard_id
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid import ID format",
			"Expected format: instance_id:shard_id (e.g. r-xxxxx:r-xxxxx-db-0)",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("instance_id"), types.StringValue(parts[0]))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("shard_id"), types.StringValue(parts[1]))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(req.ID))...)
}

// --- API helpers ---

// makeShardId builds the composite resource ID: instance_id:shard_id.
func makeShardId(instanceId, shardId string) string {
	return fmt.Sprintf("%s:%s", instanceId, shardId)
}

// setBandwidth calls EnableAdditionalBandwidth with the given shard ID and bandwidth.
// bandwidth=0 resets the shard to default (used by Delete).
//
// Per-shard bandwidth and burst are mutually exclusive per shard: setting
// per-shard bandwidth on a shard disables burst on THAT shard only. Sibling
// shards keep their burst state. No BandWidthBurst parameter is needed —
// omitting it preserves burst on siblings.
func (r *kvstoreIndividualShardBandwidthResource) setBandwidth(instanceId, shardId string, bandwidth int64) error {
	bwStr := fmt.Sprintf("%d", bandwidth)

	queries := map[string]any{
		"InstanceId":  tea.String(instanceId),
		"NodeId":      tea.String(shardId),
		"Bandwidth":   tea.String(bwStr),
		"ChargeType":  tea.String("PostPaid"),
		"AutoPay":     tea.String("true"),
	}

	_, err := kvstoreRawCall(r.client, "EnableAdditionalBandwidth", queries)
	if err != nil {
		return fmt.Errorf("failed to set individual shard bandwidth for instance %s shard %s: %w", instanceId, shardId, err)
	}

	if waitErr := kvstoreWaitForInstanceNormal(r.client, instanceId, 5*time.Minute); waitErr != nil {
		return fmt.Errorf("bandwidth set but instance %s did not return to Normal: %w", instanceId, waitErr)
	}
	return nil
}

// verifyBandwidth reads back the shard bandwidth and confirms the requested
// total value took effect. Catches cases where the API returns success but the
// change was silently ignored.
func (r *kvstoreIndividualShardBandwidthResource) verifyBandwidth(instanceId, shardId string, desiredBw, defaultBw int64) error {
	currentBw, _, _, err := kvstoreReadNodeBandwidth(r.client, instanceId, shardId)
	if err != nil {
		return fmt.Errorf("failed to read node bandwidth for verification: %w", err)
	}
	if currentBw != desiredBw {
		return fmt.Errorf("bandwidth mismatch for shard %s: requested %d MB/s total, got %d MB/s (DefaultBandWidth=%d, additional=%d)",
			shardId, desiredBw, currentBw, defaultBw, desiredBw-defaultBw)
	}
	return nil
}
