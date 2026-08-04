package utils

import (
	"fmt"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/cenkalti/backoff/v4"

	alicloudKvstoreClient "github.com/alibabacloud-go/r-kvstore-20150101/v7/client"
)

// KvstoreWaitForInstanceNormal polls DescribeInstances until the instance
// reaches Normal status or the timeout elapses. Redis bandwidth changes take
// 1-2 minutes (instance goes through Changing → Normal).
// Returns nil if the instance is deleted (empty result) — caller should treat
// this as "nothing to do" rather than an error.
func KvstoreWaitForInstanceNormal(client *alicloudKvstoreClient.Client, instanceId string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pollInterval := 10 * time.Second

	for time.Now().Before(deadline) {
		var resp *alicloudKvstoreClient.DescribeInstancesResponse
		readFn := func() error {
			r, e := client.DescribeInstances(&alicloudKvstoreClient.DescribeInstancesRequest{
				InstanceIds: tea.String(instanceId),
			})
			resp = r
			return e
		}
		reconnectBackoff := backoff.NewExponentialBackOff()
		reconnectBackoff.MaxElapsedTime = 5 * time.Minute
		err := backoff.Retry(func() error {
			err := readFn()
			if err == nil {
				return nil
			}
			if t, ok := err.(*tea.SDKError); ok {
				if IsAbleToRetry(*t.Code) {
					return err
				}
				return backoff.Permanent(err)
			}
			return backoff.Permanent(err)
		}, reconnectBackoff)
		if err != nil {
			// Instance deleted — nothing to wait for.
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "notfound") || strings.Contains(errStr, "invalidinstance") {
				return nil
			}
		}
		if err == nil && resp != nil && resp.Body != nil && resp.Body.Instances != nil {
			kvInsts := resp.Body.Instances.KVStoreInstance
			if len(kvInsts) == 0 {
				// Instance deleted — not in the list.
				return nil
			}
			if kvInsts[0].InstanceStatus != nil && *kvInsts[0].InstanceStatus == "Normal" {
				return nil
			}
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("timed out waiting for instance %s to reach Normal status", instanceId)
}

// KvstoreInstanceExists checks if a Redis instance still exists.
func KvstoreInstanceExists(client *alicloudKvstoreClient.Client, instanceId string) bool {
	resp, err := client.DescribeInstances(&alicloudKvstoreClient.DescribeInstancesRequest{
		InstanceIds: tea.String(instanceId),
	})
	if err != nil {
		return false
	}
	if resp == nil || resp.Body == nil || resp.Body.Instances == nil {
		return false
	}
	return len(resp.Body.Instances.KVStoreInstance) > 0
}

// KvstoreReadBurstValue reads IntranetBandWidthBurst from DescribeIntranetAttribute.
// Returns the burst cap in MB/s. 0 = burst disabled.
func KvstoreReadBurstValue(client *alicloudKvstoreClient.Client, instanceId string) (int64, error) {
	var resp *alicloudKvstoreClient.DescribeIntranetAttributeResponse
	readFn := func() error {
		r, e := client.DescribeIntranetAttribute(&alicloudKvstoreClient.DescribeIntranetAttributeRequest{
			InstanceId: tea.String(instanceId),
		})
		resp = r
		return e
	}
	reconnectBackoff := backoff.NewExponentialBackOff()
	reconnectBackoff.MaxElapsedTime = 5 * time.Minute
	err := backoff.Retry(func() error {
		err := readFn()
		if err == nil {
			return nil
		}
		if t, ok := err.(*tea.SDKError); ok {
			if IsAbleToRetry(*t.Code) {
				return err
			}
			return backoff.Permanent(err)
		}
		return backoff.Permanent(err)
	}, reconnectBackoff)
	if err != nil {
		return 0, err
	}
	if resp == nil || resp.Body == nil {
		return 0, fmt.Errorf("empty response from DescribeIntranetAttribute for %s", instanceId)
	}
	if resp.Body.IntranetBandWidthBurst == nil {
		return 0, nil
	}
	return int64(*resp.Body.IntranetBandWidthBurst), nil
}

// KvstoreReadNodeBandwidth reads individual shard bandwidth from DescribeRoleZoneInfo,
// matching by InsName (e.g. "r-xxx-db-0"). Returns currentBw, defaultBw, isBwOpen.
func KvstoreReadNodeBandwidth(client *alicloudKvstoreClient.Client, instanceId, shardId string) (currentBw, defaultBw int64, isBwOpen bool, err error) {
	var resp *alicloudKvstoreClient.DescribeRoleZoneInfoResponse
	readFn := func() error {
		r, e := client.DescribeRoleZoneInfo(&alicloudKvstoreClient.DescribeRoleZoneInfoRequest{
			InstanceId: tea.String(instanceId),
		})
		resp = r
		return e
	}
	reconnectBackoff := backoff.NewExponentialBackOff()
	reconnectBackoff.MaxElapsedTime = 5 * time.Minute
	err = backoff.Retry(func() error {
		err := readFn()
		if err == nil {
			return nil
		}
		if t, ok := err.(*tea.SDKError); ok {
			if IsAbleToRetry(*t.Code) {
				return err
			}
			return backoff.Permanent(err)
		}
		return backoff.Permanent(err)
	}, reconnectBackoff)
	if err != nil {
		return 0, 0, false, fmt.Errorf("failed to read node bandwidth for shard %s: %w", shardId, err)
	}
	if resp == nil || resp.Body == nil || resp.Body.Node == nil {
		return 0, 0, false, fmt.Errorf("no Node in response for instance %s", instanceId)
	}

	nodes := resp.Body.Node.NodeInfo
	if len(nodes) == 0 {
		return 0, 0, false, fmt.Errorf("no NodeInfo in response for instance %s", instanceId)
	}

	// Match by InsName (e.g. "r-xxx-db-0") — this is the format EnableAdditionalBandwidth expects.
	// DescribeRoleZoneInfo returns both MASTER and SLAVE for each shard; we take the first match
	// (usually MASTER) since bandwidth is identical for both.
	for _, node := range nodes {
		if node.InsName != nil && *node.InsName == shardId {
			if node.CurrentBandWidth != nil {
				currentBw = *node.CurrentBandWidth
			}
			if node.DefaultBandWidth != nil {
				defaultBw = *node.DefaultBandWidth
			}
			if node.IsOpenBandWidthService != nil {
				isBwOpen = *node.IsOpenBandWidthService
			}
			return currentBw, defaultBw, isBwOpen, nil
		}
	}

	return 0, 0, false, fmt.Errorf("node %s not found in instance %s (matched by InsName)", shardId, instanceId)
}
