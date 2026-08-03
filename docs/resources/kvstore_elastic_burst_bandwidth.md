---
subcategory: "Redis (R-Kvstore)"
layout: "alicloud"
page_title: "ST-Alicloud: kvstore_elastic_burst_bandwidth"
description: |-
  Manages elastic burst bandwidth for an Alibaba Cloud Redis instance.
---

# st-alicloud_kvstore_elastic_burst_bandwidth

Manages elastic burst bandwidth for an Alibaba Cloud Redis (R-Kvstore) instance.

Burst allows the instance to temporarily exceed its base bandwidth limit. This is an **instance-level** setting — it applies to all shards.

## Example Usage

```hcl
resource "st-alicloud_kvstore_elastic_burst_bandwidth" "burst" {
  instance_id         = "r-xxxxx"
  burstable_bandwidth = true
}
```

## Argument Reference

The following arguments are supported:

* `instance_id` - (Required, Forces new resource) The ID of the Redis instance.
* `burstable_bandwidth` - (Required) Whether to enable elastic burst bandwidth. Set to `true` to enable burst, `false` to disable.

## Attribute Reference

The following attributes are exported:

* `id` - The resource ID (same as `instance_id`).

## Notes

* Burst is an instance-level setting — when enabled, the instance can temporarily exceed its bandwidth limit.
* This resource uses the `EnableAdditionalBandwidth` API with `NodeId="All"`.
* Deleting the resource disables burst.
* If the Redis instance is already destroyed, `Delete` is a no-op (no error).
* The provider waits for instance `Normal` status before and after the API call (up to 10 min before, 5 min after).
* `Task.Conflict` errors (unfinished task) are retried with exponential backoff up to 5 minutes.
* Burst attribute (`IntranetBandWidthBurst`) propagation may lag behind instance `Normal` status. `verifyBurst` polls every 10 seconds up to 5 minutes.
* The API may take up to 5 minutes to complete as the instance goes through `Changing` → `Normal` status.
* If applying this resource together with `st-alicloud_kvstore_individual_shard_bandwidth` on the same instance, add `depends_on = [st-alicloud_kvstore_elastic_burst_bandwidth.burst]` to the individual shard resource. Enabling burst wipes existing per-shard bandwidth, so per-shard must run **after** burst. The per-shard call naturally preserves burst on sibling shards.
