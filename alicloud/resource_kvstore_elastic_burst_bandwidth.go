package alicloud

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	alicloudOpenapiClient "github.com/alibabacloud-go/darabonba-openapi/v2/client"
)

var (
	_ resource.Resource                = &kvstoreElasticBurstBandwidthResource{}
	_ resource.ResourceWithConfigure   = &kvstoreElasticBurstBandwidthResource{}
	_ resource.ResourceWithImportState = &kvstoreElasticBurstBandwidthResource{}
)

func NewKvstoreElasticBurstBandwidthResource() resource.Resource {
	return &kvstoreElasticBurstBandwidthResource{}
}

type kvstoreElasticBurstBandwidthResource struct {
	client *alicloudOpenapiClient.Client
}

type kvstoreElasticBurstBandwidthModel struct {
	Id                 types.String `tfsdk:"id"`
	InstanceId         types.String `tfsdk:"instance_id"`
	BurstableBandwidth types.Bool   `tfsdk:"burstable_bandwidth"`
}

func (r *kvstoreElasticBurstBandwidthResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kvstore_elastic_burst_bandwidth"
}

func (r *kvstoreElasticBurstBandwidthResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages elastic burst bandwidth for an Alibaba Cloud Redis (R-Kvstore) instance. " +
			"Burst allows the instance to temporarily exceed its base bandwidth limit. " +
			"This is an instance-level setting — it applies to all shards.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The resource ID (same as instance_id).",
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
			"burstable_bandwidth": schema.BoolAttribute{
				Description: "Whether to enable elastic burst bandwidth. " +
					"Set to `true` to enable burst, `false` to disable.",
				Required: true,
			},
		},
	}
}

func (r *kvstoreElasticBurstBandwidthResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(alicloudClients).kvstoreRawClient
}

// --- CRUD ---

func (r *kvstoreElasticBurstBandwidthResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan *kvstoreElasticBurstBandwidthModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := plan.InstanceId.ValueString()
	burst := plan.BurstableBandwidth.ValueBool()

	if err := r.setBurst(instanceId, burst); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to set Redis elastic burst bandwidth.",
			err.Error(),
		)
		return
	}

	if err := r.verifyBurst(instanceId, burst); err != nil {
		resp.Diagnostics.AddError(
			"[VERIFY ERROR] Apply succeeded but read-back verification failed.",
			err.Error(),
		)
		return
	}

	state := &kvstoreElasticBurstBandwidthModel{
		Id:                 types.StringValue(instanceId),
		InstanceId:         plan.InstanceId,
		BurstableBandwidth: plan.BurstableBandwidth,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstoreElasticBurstBandwidthResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state *kvstoreElasticBurstBandwidthModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := state.InstanceId.ValueString()

	// Check instance still exists. DescribeInstances returns empty list if deleted.
	_, err := kvstoreRawCall(r.client, "DescribeInstances", map[string]any{
		"InstanceIds": tea.String(instanceId),
	})
	if err != nil {
		// Instance may be gone — remove from state.
		if strings.Contains(strings.ToLower(err.Error()), "notfound") ||
			strings.Contains(strings.ToLower(err.Error()), "invalidinstance") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to read Redis instance.",
			err.Error(),
		)
		return
	}

	state.Id = types.StringValue(instanceId)
	// Keep the plan/state value for burstable_bandwidth — do NOT override from API.
	// The API read-back may be stale immediately after apply, causing diff loops.

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstoreElasticBurstBandwidthResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan *kvstoreElasticBurstBandwidthModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := plan.InstanceId.ValueString()
	burst := plan.BurstableBandwidth.ValueBool()

	if err := r.setBurst(instanceId, burst); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to update Redis elastic burst bandwidth.",
			err.Error(),
		)
		return
	}

	if err := r.verifyBurst(instanceId, burst); err != nil {
		resp.Diagnostics.AddError(
			"[VERIFY ERROR] Update succeeded but read-back verification failed.",
			err.Error(),
		)
		return
	}

	state := &kvstoreElasticBurstBandwidthModel{
		Id:                 types.StringValue(instanceId),
		InstanceId:         plan.InstanceId,
		BurstableBandwidth: plan.BurstableBandwidth,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *kvstoreElasticBurstBandwidthResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state *kvstoreElasticBurstBandwidthModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	instanceId := state.InstanceId.ValueString()

	// Disable burst
	if err := r.setBurst(instanceId, false); err != nil {
		resp.Diagnostics.AddError(
			"[API ERROR] Failed to disable Redis elastic burst bandwidth.",
			err.Error(),
		)
		return
	}
}

func (r *kvstoreElasticBurstBandwidthResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Format: instance_id
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("instance_id"), types.StringValue(req.ID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(req.ID))...)
}

// --- API helpers ---

// setBurst calls EnableAdditionalBandwidth with NodeId="All" to toggle burst.
//
// CRITICAL: Before the API call, reads the current individual shard bandwidth state.
// Burst and individual shard bandwidth are mutually exclusive per shard — calling
// EnableAdditionalBandwidth(NodeId="All", Bandwidth=0) without preserving
// existing individual shard bandwidths silently wipes them. To preserve individual shard
// additional bandwidth on shards that have it, we read the current state and
// pass the shard IDs and bandwidths through.
//
// If any shard has additional bandwidth set, we include those shard IDs and
// their bandwidth values in the call alongside NodeId="All" for burst.
func (r *kvstoreElasticBurstBandwidthResource) setBurst(instanceId string, burst bool) error {
	burstStr := "false"
	if burst {
		burstStr = "true"
	}

	// Read current individual shard bandwidths to preserve them.
	shards, _, _ := kvstoreReadAllShardBandwidths(r.client, instanceId)

	queries := map[string]any{
		"InstanceId":     tea.String(instanceId),
		"NodeId":         tea.String("All"),
		"Bandwidth":      tea.String("0"),
		"BandWidthBurst": tea.String(burstStr),
		"ChargeType":     tea.String("PostPaid"),
		"AutoPay":        tea.String("true"),
	}

	// If shards have individual shard additional bandwidth, include their IDs and
	// bandwidth values so the API preserves them. The API accepts multiple
	// shard IDs comma-separated in NodeId, with matching Bandwidth values.
	if len(shards) > 0 {
		var nodeIds []string
		var bwVals []string
		for _, s := range shards {
			if s.AdditionalBw > 0 {
				nodeIds = append(nodeIds, s.ShardId)
				bwVals = append(bwVals, fmt.Sprintf("%d", s.AdditionalBw))
			}
		}
		if len(nodeIds) > 0 {
			queries["NodeId"] = tea.String("All," + strings.Join(nodeIds, ","))
			queries["Bandwidth"] = tea.String("0," + strings.Join(bwVals, ","))
		}
	}

	_, err := kvstoreRawCall(r.client, "EnableAdditionalBandwidth", queries)
	if err != nil {
		return fmt.Errorf("failed to set elastic burst for instance %s: %w", instanceId, err)
	}

	if waitErr := kvstoreWaitForInstanceNormal(r.client, instanceId, 5*time.Minute); waitErr != nil {
		return fmt.Errorf("burst set but instance %s did not return to Normal: %w", instanceId, waitErr)
	}
	return nil
}

// verifyBurst reads back IntranetBandWidthBurst and confirms the burst state matches.
func (r *kvstoreElasticBurstBandwidthResource) verifyBurst(instanceId string, burst bool) error {
	burstBw, err := kvstoreReadBurstValue(r.client, instanceId)
	if err != nil {
		return fmt.Errorf("failed to read burst status for verification: %w", err)
	}
	if burst && burstBw <= 0 {
		return fmt.Errorf("burstable_bandwidth=true but instance %s burst is not enabled (IntranetBandWidthBurst=0)", instanceId)
	}
	if !burst && burstBw > 0 {
		return fmt.Errorf("burstable_bandwidth=false but instance %s burst is still enabled (IntranetBandWidthBurst=%d)", instanceId, burstBw)
	}
	return nil
}
