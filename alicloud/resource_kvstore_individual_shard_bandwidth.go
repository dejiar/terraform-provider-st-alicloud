package alicloud

import (
	"github.com/myklst/terraform-provider-st-alicloud/utils"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	alicloudKvstoreClient "github.com/alibabacloud-go/r-kvstore-20150101/v7/client"
)

var (
	_ resource.Resource              = &kvstoreIndividualShardBandwidthResource{}
	_ resource.ResourceWithConfigure = &kvstoreIndividualShardBandwidthResource{}
)

func NewKvstoreIndividualShardBandwidthResource() resource.Resource {
	return &kvstoreIndividualShardBandwidthResource{}
}

type kvstoreIndividualShardBandwidthResource struct {
	client *alicloudKvstoreClient.Client
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
	r.client = req.ProviderData.(alicloudClients).kvstoreClient
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
	_, defaultBw, _, err := utils.KvstoreReadNodeBandwidth(r.client, instanceId, shardId)
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

	currentBw, _, _, err := utils.KvstoreReadNodeBandwidth(r.client, instanceId, shardId)
	if err != nil {
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

	// Keep the plan/state value for bandwidth — do NOT override from API.
	// The API read-back may be stale immediately after apply, causing diff loops.
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
	_, defaultBw, _, err := utils.KvstoreReadNodeBandwidth(r.client, instanceId, shardId)
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

	// If the Redis instance itself is gone, nothing to reset.
	if !utils.KvstoreInstanceExists(r.client, instanceId) {
		return
	}

	// Reset shard bandwidth to 0 (default).
	if err := r.setBandwidth(instanceId, shardId, 0); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to reset Redis individual shard bandwidth.",
			err.Error(),
		)
		return
	}
}

// --- API helpers ---

// makeShardId builds the composite resource ID: instance_id:shard_id.
func makeShardId(instanceId, shardId string) string {
	return fmt.Sprintf("%s:%s", instanceId, shardId)
}

// setBandwidth calls EnableAdditionalBandwidth with the given shard ID and bandwidth.
// bandwidth=0 resets the shard to default (used by Delete).
func (r *kvstoreIndividualShardBandwidthResource) setBandwidth(instanceId, shardId string, bandwidth int64) error {
	// Wait for any in-flight task to finish before making changes.
	// AliCloud Redis returns "current instance has unfinish task" if the
	// instance is still in "Changing" status from a previous operation.
	if waitErr := utils.KvstoreWaitForInstanceNormal(r.client, instanceId, 10*time.Minute); waitErr != nil {
		return fmt.Errorf("instance %s not in Normal state before setBandwidth: %w", instanceId, waitErr)
	}

	req := &alicloudKvstoreClient.EnableAdditionalBandwidthRequest{
		InstanceId:  tea.String(instanceId),
		NodeId:      tea.String(shardId),
		Bandwidth:   tea.String(fmt.Sprintf("%d", bandwidth)),
		ChargeType:  tea.String("PostPaid"),
		AutoPay:     tea.Bool(true),
	}

	enableFn := func() error {
		_, e := r.client.EnableAdditionalBandwidth(req)
		return e
	}
	reconnectBackoff := backoff.NewExponentialBackOff()
	reconnectBackoff.MaxElapsedTime = 5 * time.Minute
	err := backoff.Retry(func() error {
		err := enableFn()
		if err == nil {
			return nil
		}
		if t, ok := err.(*tea.SDKError); ok {
			if utils.IsAbleToRetry(tea.StringValue(t.Code)) {
				return err
			}
			return backoff.Permanent(err)
		}
		return backoff.Permanent(err)
	}, reconnectBackoff)
	if err != nil {
		return fmt.Errorf("failed to set individual shard bandwidth for instance %s shard %s: %w", instanceId, shardId, err)
	}

	if waitErr := utils.KvstoreWaitForInstanceNormal(r.client, instanceId, 5*time.Minute); waitErr != nil {
		return fmt.Errorf("bandwidth set but instance %s did not return to Normal: %w", instanceId, waitErr)
	}
	return nil
}

// verifyBandwidth reads back the shard bandwidth and confirms the requested
// total value took effect.
func (r *kvstoreIndividualShardBandwidthResource) verifyBandwidth(instanceId, shardId string, desiredBw, defaultBw int64) error {
	currentBw, _, _, err := utils.KvstoreReadNodeBandwidth(r.client, instanceId, shardId)
	if err != nil {
		return fmt.Errorf("failed to read node bandwidth for verification: %w", err)
	}
	if currentBw != desiredBw {
		return fmt.Errorf("bandwidth mismatch for shard %s: requested %d MB/s total, got %d MB/s (DefaultBandWidth=%d, additional=%d)",
			shardId, desiredBw, currentBw, defaultBw, desiredBw-defaultBw)
	}
	return nil
}
