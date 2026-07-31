package alicloud

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/cenkalti/backoff/v4"

	alicloudOpenapiClient "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	openapiutil "github.com/alibabacloud-go/openapi-util/service"
)

// kvstoreRawCall performs a raw CallApi against the R-Kvstore API using the v2
// openapi client. Retries on transient errors via isAbleToRetry.
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
