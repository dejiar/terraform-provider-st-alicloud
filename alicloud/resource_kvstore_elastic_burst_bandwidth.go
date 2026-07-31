package alicloud

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	alicloudKvstoreClient "github.com/alibabacloud-go/r-kvstore-20150101/v7/client"
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
	client *alicloudKvstoreClient.Client
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
	r.client = req.ProviderData.(alicloudClients).kvstoreClient
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

	// Check instance still exists.
	err := kvstoreRetry(func() error {
		_, e := r.client.DescribeInstances(&alicloudKvstoreClient.DescribeInstancesRequest{
			InstanceIds: tea.String(instanceId),
		})
		return e
	})
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "notfound") || strings.Contains(errStr, "invalidinstance") {
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

// setBurst toggles elastic burst bandwidth while preserving the existing
// bandwidth configuration (instance-level or per-shard).
func (r *kvstoreElasticBurstBandwidthResource) setBurst(instanceId string, burst bool) error {
	nodeId, bandwidth, err := r.classifyAndBuildBwParams(instanceId)
	if err != nil {
		return fmt.Errorf("failed to classify bandwidth state for instance %s: %w", instanceId, err)
	}

	req := &alicloudKvstoreClient.EnableAdditionalBandwidthRequest{
		InstanceId:     tea.String(instanceId),
		NodeId:         tea.String(nodeId),
		Bandwidth:      tea.String(bandwidth),
		BandWidthBurst: tea.Bool(burst),
		ChargeType:     tea.String("PostPaid"),
		AutoPay:        tea.Bool(true),
	}

	if err := kvstoreEnableAdditionalBandwidth(r.client, req); err != nil {
		return fmt.Errorf("failed to set elastic burst for instance %s (NodeId=%s, Bandwidth=%s): %w",
			instanceId, nodeId, bandwidth, err)
	}

	if waitErr := kvstoreWaitForInstanceNormal(r.client, instanceId, 5*time.Minute); waitErr != nil {
		return fmt.Errorf("burst set but instance %s did not return to Normal: %w", instanceId, waitErr)
	}

	return nil
}

// classifyAndBuildBwParams reads DescribeRoleZoneInfo and determines the
// correct NodeId and Bandwidth parameters to preserve the current bandwidth
// state when toggling burst.
func (r *kvstoreElasticBurstBandwidthResource) classifyAndBuildBwParams(instanceId string) (nodeId, bandwidth string, err error) {
	var resp *alicloudKvstoreClient.DescribeRoleZoneInfoResponse
	err = kvstoreRetry(func() error {
		r, e := r.client.DescribeRoleZoneInfo(&alicloudKvstoreClient.DescribeRoleZoneInfoRequest{
			InstanceId: tea.String(instanceId),
		})
		resp = r
		return e
	})
	if err != nil {
		return "", "", fmt.Errorf("DescribeRoleZoneInfo failed: %w", err)
	}
	if resp == nil || resp.Body == nil || resp.Body.Node == nil {
		return "", "", fmt.Errorf("no Node in DescribeRoleZoneInfo response")
	}

	nodes := resp.Body.Node.NodeInfo
	if len(nodes) == 0 {
		return "", "", fmt.Errorf("no NodeInfo in DescribeRoleZoneInfo response")
	}

	// Collect one entry per unique InsName. DescribeRoleZoneInfo returns both
	// MASTER and SLAVE for each shard with identical bandwidth — take the
	// first occurrence (whichever role appears first).
	seen := make(map[string]bool)
	type shardBw struct {
		InsName   string
		CurrentBw int64
		DefaultBw int64
	}
	var shards []shardBw
	for _, node := range nodes {
		if node.InsName == nil {
			continue
		}
		insName := *node.InsName
		if insName == "" || seen[insName] {
			continue
		}
		seen[insName] = true
		var curBw, defBw int64
		if node.CurrentBandWidth != nil {
			curBw = *node.CurrentBandWidth
		}
		if node.DefaultBandWidth != nil {
			defBw = *node.DefaultBandWidth
		}
		shards = append(shards, shardBw{
			InsName:   insName,
			CurrentBw: curBw,
			DefaultBw: defBw,
		})
	}

	if len(shards) == 0 {
		return "", "", fmt.Errorf("no master shards found in DescribeRoleZoneInfo")
	}

	// Scenario 1: all shards current == default → no bandwidth adjustment.
	allMatchDefault := true
	for _, s := range shards {
		if s.CurrentBw != s.DefaultBw {
			allMatchDefault = false
			break
		}
	}
	if allMatchDefault {
		return "All", "0", nil
	}

	// Scenario 2: all shards share the same current and default values.
	allSameCurrent := true
	firstCurrent := shards[0].CurrentBw
	firstDefault := shards[0].DefaultBw
	for _, s := range shards {
		if s.CurrentBw != firstCurrent || s.DefaultBw != firstDefault {
			allSameCurrent = false
			break
		}
	}
	if allSameCurrent {
		additional := firstCurrent - firstDefault
		if additional < 0 {
			additional = 0
		}
		return "All", strconv.FormatInt(additional, 10), nil
	}

	// Scenario 3: per-shard adjustment — shards have different current values.
	var ids []string
	var bws []string
	for _, s := range shards {
		additional := s.CurrentBw - s.DefaultBw
		if additional < 0 {
			additional = 0
		}
		ids = append(ids, s.InsName)
		bws = append(bws, strconv.FormatInt(additional, 10))
	}
	return strings.Join(ids, ","), strings.Join(bws, ","), nil
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
