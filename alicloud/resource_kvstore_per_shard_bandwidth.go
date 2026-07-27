package alicloud

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	_ resource.Resource                = &kvstorePerShardBandwidthResource{}
	_ resource.ResourceWithConfigure   = &kvstorePerShardBandwidthResource{}
	_ resource.ResourceWithImportState = &kvstorePerShardBandwidthResource{}
)

func NewKvstorePerShardBandwidthResource() resource.Resource {
	return &kvstorePerShardBandwidthResource{}
}

type kvstorePerShardBandwidthResource struct {
	client *alicloudOpenapiClient.Client
}

type kvstorePerShardBandwidthModel struct {
	Id         types.String `tfsdk:"id"`
	InstanceId types.String `tfsdk:"instance_id"`
	ShardId    types.String `tfsdk:"shard_id"`
	Bandwidth  types.Int64  `tfsdk:"bandwidth"`
}

func (r *kvstorePerShardBandwidthResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kvstore_per_shard_bandwidth"
}

func (r *kvstorePerShardBandwidthResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages additional per-shard bandwidth for an Alibaba Cloud Redis (R-Kvstore) instance. " +
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
					"Use `DescribeRoleZoneInfo` or `DescribeLogicInstanceTopology` to list available shard IDs.",
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"bandwidth": schema.Int64Attribute{
				Description: "Additional bandwidth in MB/s for the shard. Must be a positive integer (>= 1). " +
					"The max per-shard additional bandwidth is `IntranetBandWidthBurst - DefaultBandWidth` " +
					"(both read from the API). Validate this in your Terraform `variable` `validation` blocks.",
				Required: true,
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
		},
	}
}

func (r *kvstorePerShardBandwidthResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(alicloudClients).kvstoreRawClient
}

// --- CRUD ---

func (r *kvstorePerShardBandwidthResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan *kvstorePerShardBandwidthModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := plan.InstanceId.ValueString()
	shardId := plan.ShardId.ValueString()
	bandwidth := plan.Bandwidth.ValueInt64()

	if err := r.setBandwidth(instanceId, shardId, bandwidth); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to set Redis per-shard bandwidth.",
			err.Error(),
		)
		return
	}

	if err := r.verifyBandwidth(instanceId, shardId, bandwidth); err != nil {
		resp.Diagnostics.AddError(
			"[VERIFY ERROR] Apply succeeded but read-back verification failed.",
			err.Error(),
		)
		return
	}

	state := &kvstorePerShardBandwidthModel{
		Id:         types.StringValue(makeShardId(instanceId, shardId)),
		InstanceId: plan.InstanceId,
		ShardId:    plan.ShardId,
		Bandwidth:  plan.Bandwidth,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstorePerShardBandwidthResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state *kvstorePerShardBandwidthModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := state.InstanceId.ValueString()
	shardId := state.ShardId.ValueString()

	currentBw, defaultBw, _, err := kvstoreReadNodeBandwidth(r.client, instanceId, shardId)
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
			"[API ERROR] Failed to read Redis per-shard bandwidth.",
			err.Error(),
		)
		return
	}

	state.Id = types.StringValue(makeShardId(instanceId, shardId))

	// During import, bandwidth is null — compute from API.
	// Otherwise keep the plan/state value (do NOT override — prevents diff loops).
	if state.Bandwidth.IsNull() {
		additional := currentBw - defaultBw
		if additional < 0 {
			additional = 0
		}
		state.Bandwidth = types.Int64Value(additional)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstorePerShardBandwidthResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan *kvstorePerShardBandwidthModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := plan.InstanceId.ValueString()
	shardId := plan.ShardId.ValueString()
	bandwidth := plan.Bandwidth.ValueInt64()

	if err := r.setBandwidth(instanceId, shardId, bandwidth); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to update Redis per-shard bandwidth.",
			err.Error(),
		)
		return
	}

	if err := r.verifyBandwidth(instanceId, shardId, bandwidth); err != nil {
		resp.Diagnostics.AddError(
			"[VERIFY ERROR] Update succeeded but read-back verification failed.",
			err.Error(),
		)
		return
	}

	state := &kvstorePerShardBandwidthModel{
		Id:         types.StringValue(makeShardId(instanceId, shardId)),
		InstanceId: plan.InstanceId,
		ShardId:    plan.ShardId,
		Bandwidth:  plan.Bandwidth,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstorePerShardBandwidthResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state *kvstorePerShardBandwidthModel
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
			"[API ERROR] Failed to reset Redis per-shard bandwidth.",
			err.Error(),
		)
		return
	}
}

func (r *kvstorePerShardBandwidthResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
// CRITICAL: Before the API call, reads the current burst state for the instance.
// Per-shard bandwidth and burst are mutually exclusive per shard — calling
// EnableAdditionalBandwidth on a shard without BandWidthBurst silently disables
// burst on that shard. To preserve burst on shards that still have it, we read
// the instance-level burst value and pass it through.
//
// If burst is currently enabled at instance level (NodeId="All"), we keep
// BandWidthBurst=true so the API preserves it on the target shard's sibling
// shards. The target shard itself switches from burst to per-shard additional.
func (r *kvstorePerShardBandwidthResource) setBandwidth(instanceId, shardId string, bandwidth int64) error {
	bwStr := fmt.Sprintf("%d", bandwidth)

	// Read current burst state to preserve it on sibling shards.
	burstBw, _ := kvstoreReadBurstValue(r.client, instanceId)
	burstStr := "false"
	if burstBw > 0 {
		burstStr = "true"
	}

	queries := map[string]any{
		"InstanceId":     tea.String(instanceId),
		"NodeId":         tea.String(shardId),
		"Bandwidth":      tea.String(bwStr),
		"BandWidthBurst": tea.String(burstStr),
		"ChargeType":     tea.String("PostPaid"),
		"AutoPay":        tea.String("true"),
	}

	_, err := kvstoreRawCall(r.client, "EnableAdditionalBandwidth", queries)
	if err != nil {
		return fmt.Errorf("failed to set per-shard bandwidth for instance %s shard %s: %w", instanceId, shardId, err)
	}

	if waitErr := kvstoreWaitForInstanceNormal(r.client, instanceId, 5*time.Minute); waitErr != nil {
		return fmt.Errorf("bandwidth set but instance %s did not return to Normal: %w", instanceId, waitErr)
	}
	return nil
}

// verifyBandwidth reads back the shard bandwidth and confirms the requested
// value took effect. Catches cases where the API returns success but the
// change was silently ignored.
func (r *kvstorePerShardBandwidthResource) verifyBandwidth(instanceId, shardId string, bandwidth int64) error {
	currentBw, defaultBw, _, err := kvstoreReadNodeBandwidth(r.client, instanceId, shardId)
	if err != nil {
		return fmt.Errorf("failed to read node bandwidth for verification: %w", err)
	}
	actual := currentBw - defaultBw
	if actual < 0 {
		actual = 0
	}
	if actual != bandwidth {
		return fmt.Errorf("bandwidth mismatch for shard %s: requested %d MB/s, got %d MB/s (CurrentBandWidth=%d, DefaultBandWidth=%d)",
			shardId, bandwidth, actual, currentBw, defaultBw)
	}
	return nil
}
