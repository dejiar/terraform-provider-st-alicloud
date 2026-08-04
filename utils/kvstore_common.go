package utils

import (
	"fmt"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/dara"
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
			runtime := &dara.RuntimeOptions{}
			r, e := client.DescribeInstancesWithOptions(&alicloudKvstoreClient.DescribeInstancesRequest{
				InstanceIds: tea.String(instanceId),
			}, runtime)
			resp = r
			if e != nil {
				if _t, ok := e.(*tea.SDKError); ok {
					if IsAbleToRetry(*_t.Code) {
						return e
					} else {
						return backoff.Permanent(e)
					}
				} else {
					return e
				}
			}
			return nil
		}

		// Retry backoff
		reconnectBackoff := backoff.NewExponentialBackOff()
		reconnectBackoff.MaxElapsedTime = 5 * time.Minute
		err := backoff.Retry(readFn, reconnectBackoff)
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
	runtime := &dara.RuntimeOptions{}
	resp, err := client.DescribeInstancesWithOptions(&alicloudKvstoreClient.DescribeInstancesRequest{
		InstanceIds: tea.String(instanceId),
	}, runtime)
	if err != nil {
		return false
	}
	if resp == nil || resp.Body == nil || resp.Body.Instances == nil {
		return false
	}
	return len(resp.Body.Instances.KVStoreInstance) > 0
}


