package alicloud

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/cenkalti/backoff/v4"

	alicloudOpenapiClient "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	openapiutil "github.com/alibabacloud-go/openapi-util/service"
)

// kvstoreShardBw holds the bandwidth state of a single shard, read from
// DescribeLogicInstanceTopology. AdditionalBw is the individual shard additional
// bandwidth (current - base), or 0 if burst is active on the shard (burst
// replaces additional bandwidth — they are mutually exclusive per shard).
type kvstoreShardBw struct {
	ShardId      string // e.g. "r-xxx-db-0" (NodeId with # suffix stripped)
	CurrentBw    int64  // total bandwidth shown in topology
	AdditionalBw int64  // individual shard additional bandwidth (0 if burst active)
}

// kvstoreReadAllShardBandwidths reads the current individual shard bandwidth state
// from DescribeLogicInstanceTopology. Returns the list of shards (master nodes
// only) and the base bandwidth per shard.
//
// Used by the burst resource to preserve existing individual shard bandwidth settings
// when toggling burst — without this, EnableAdditionalBandwidth(NodeId="All",
// Bandwidth=0) silently wipes individual shard additional bandwidth.
func kvstoreReadAllShardBandwidths(client *alicloudOpenapiClient.Client, instanceId string) ([]kvstoreShardBw, int64, error) {
	// 1. Read instance-level Bandwidth + ShardCount to calculate base per shard.
	instBody, err := kvstoreRawCall(client, "DescribeInstances", map[string]any{
		"InstanceIds": tea.String(instanceId),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read instance %s: %w", instanceId, err)
	}
	instsContainer, ok := instBody["Instances"].(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("no Instances in response for %s", instanceId)
	}
	insts, ok := instsContainer["KVStoreInstance"].([]any)
	if !ok || len(insts) == 0 {
		return nil, 0, fmt.Errorf("instance %s not found", instanceId)
	}
	inst, ok := insts[0].(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("invalid instance response for %s", instanceId)
	}
	totalBw := toInt64(inst["Bandwidth"])
	shardCount := toInt64(inst["ShardCount"])
	if shardCount <= 0 {
		shardCount = 1
	}
	baseBw := totalBw / shardCount

	// 2. Read shard topology.
	topo, err := kvstoreRawCall(client, "DescribeLogicInstanceTopology", map[string]any{
		"InstanceId": tea.String(instanceId),
	})
	if err != nil {
		return nil, baseBw, fmt.Errorf("failed to read topology for %s: %w", instanceId, err)
	}

	shardList, ok := topo["RedisShardList"].(map[string]any)
	if !ok {
		return nil, baseBw, nil // no shards (standard instance)
	}
	nodes, ok := shardList["NodeInfo"].([]any)
	if !ok {
		return nil, baseBw, nil
	}

	// 3. Extract master db nodes and compute additional bandwidth.
	var result []kvstoreShardBw
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if nodeType, _ := node["NodeType"].(string); nodeType != "db" {
			continue
		}
		if subType, _ := node["SubInstanceType"].(string); subType != "master" {
			continue
		}
		nodeIdRaw, _ := node["NodeId"].(string)
		shardId := nodeIdRaw
		if idx := strings.Index(shardId, "#"); idx > 0 {
			shardId = shardId[:idx]
		}
		currentBw := toInt64(node["Bandwidth"])
		additional := currentBw - baseBw
		if additional < 0 {
			additional = 0
		}
		// If current >= base * 4, burst is active on this shard — burst
		// replaces additional bandwidth, so additional is 0.
		if baseBw > 0 && currentBw >= baseBw*4 {
			additional = 0
		}
		result = append(result, kvstoreShardBw{
			ShardId:      shardId,
			CurrentBw:    currentBw,
			AdditionalBw: additional,
		})
	}

	return result, baseBw, nil
}

// kvstoreRawCall performs a raw CallApi against the R-Kvstore API using the v2
// openapi client (the v1 SDK client's CallApi signing is broken — see the
// alibaba-cloud skill reference). Retries on transient errors via isAbleToRetry.
// Returns the response body (map[string]any) extracted from under the "body" key.
func kvstoreRawCall(client *alicloudOpenapiClient.Client, action string, queries map[string]any) (map[string]any, error) {
	callFn := func() (map[string]any, error) {
		runtime := &util.RuntimeOptions{}
		params := &alicloudOpenapiClient.Params{
			Action:      tea.String(action),
			Version:     tea.String("2015-01-01"),
			Protocol:    tea.String("HTTPS"),
			Pathname:    tea.String("/"),
			Method:      tea.String("POST"),
			AuthType:    tea.String("AK"),
			BodyType:    tea.String("json"),
			ReqBodyType: tea.String("json"),
			Style:       tea.String("RPC"),
		}
		openapiReq := &alicloudOpenapiClient.OpenApiRequest{
			Query: openapiutil.Query(queries),
		}
		result, err := client.CallApi(params, openapiReq, runtime)
		if err != nil {
			if t, ok := err.(*tea.SDKError); ok {
				if isAbleToRetry(*t.Code) {
					return nil, err
				}
				return nil, backoff.Permanent(err)
			}
			return nil, err
		}
		if result == nil {
			return nil, fmt.Errorf("no response from %s", action)
		}
		body, ok := result["body"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid response body from %s", action)
		}
		return body, nil
	}

	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = 5 * time.Minute
	body, err := backoff.RetryWithData(callFn, bo)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// kvstoreWaitForInstanceNormal polls DescribeInstances until the instance
// reaches Normal status or the timeout elapses. Redis bandwidth changes take
// 1-2 minutes (instance goes through Changing → Normal).
func kvstoreWaitForInstanceNormal(client *alicloudOpenapiClient.Client, instanceId string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pollInterval := 10 * time.Second

	for time.Now().Before(deadline) {
		body, err := kvstoreRawCall(client, "DescribeInstances", map[string]any{
			"InstanceIds": tea.String(instanceId),
		})
		if err == nil {
			if insts, ok := body["Instances"].(map[string]any); ok {
				if kvInsts, ok := insts["KVStoreInstance"].([]any); ok && len(kvInsts) > 0 {
					if inst, ok := kvInsts[0].(map[string]any); ok {
						if status, ok := inst["InstanceStatus"].(string); ok && status == "Normal" {
							return nil
						}
					}
				}
			}
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("timed out waiting for instance %s to reach Normal status", instanceId)
}

// kvstoreReadBurstValue reads IntranetBandWidthBurst from DescribeIntranetAttribute.
// Returns the burst cap in MB/s. 0 = burst disabled.
// Used to verify that burst enable/disable actually took effect.
func kvstoreReadBurstValue(client *alicloudOpenapiClient.Client, instanceId string) (int64, error) {
	body, err := kvstoreRawCall(client, "DescribeIntranetAttribute", map[string]any{
		"InstanceId": tea.String(instanceId),
	})
	if err != nil {
		return 0, err
	}
	return toInt64(body["IntranetBandWidthBurst"]), nil
}

// kvstoreReadNodeBandwidth reads individual shard bandwidth from DescribeRoleZoneInfo,
// matching by InsName (e.g. "r-xxx-db-0"). Returns currentBw, defaultBw, isBwOpen.
func kvstoreReadNodeBandwidth(client *alicloudOpenapiClient.Client, instanceId, shardId string) (currentBw, defaultBw int64, isBwOpen bool, err error) {
	body, err := kvstoreRawCall(client, "DescribeRoleZoneInfo", map[string]any{
		"InstanceId": tea.String(instanceId),
	})
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to read node bandwidth for shard %s: %w", shardId, err)
	}

	nodeContainer, ok := body["Node"].(map[string]any)
	if !ok {
		return 0, 0, false, fmt.Errorf("no Node in response for instance %s", instanceId)
	}
	nodes, ok := nodeContainer["NodeInfo"].([]any)
	if !ok || len(nodes) == 0 {
		return 0, 0, false, fmt.Errorf("no NodeInfo in response for instance %s", instanceId)
	}

	// Match by InsName (e.g. "r-xxx-db-0") — this is the format EnableAdditionalBandwidth expects.
	// DescribeRoleZoneInfo returns both MASTER and SLAVE for each shard; we take the first match
	// (usually MASTER) since bandwidth is identical for both.
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		insName, ok := node["InsName"].(string)
		if !ok {
			continue
		}
		if insName == shardId {
			currentBw = toInt64(node["CurrentBandWidth"])
			defaultBw = toInt64(node["DefaultBandWidth"])
			if v, ok := node["IsOpenBandWidthService"].(bool); ok {
				isBwOpen = v
			}
			return currentBw, defaultBw, isBwOpen, nil
		}
	}

	return 0, 0, false, fmt.Errorf("node %s not found in instance %s (matched by InsName)", shardId, instanceId)
}

// toInt64 converts a value from JSON unmarshalling to int64.
// CallApi returns numeric fields as json.Number, NOT float64 — without this
// helper, IntranetBandWidthBurst reads as 0.
func toInt64(v any) int64 {
	switch val := v.(type) {
	case float64:
		return int64(val)
	case int64:
		return val
	case int:
		return int64(val)
	case json.Number:
		n, _ := val.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	}
	return 0
}
