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

## Import

Redis elastic burst bandwidth can be imported using the instance ID:

```shell
terraform import st-alicloud_kvstore_elastic_burst_bandwidth.burst r-xxxxx
```

## Notes

* Burst is an instance-level setting — when enabled, the instance can temporarily exceed its bandwidth limit.
* This resource uses the `EnableAdditionalBandwidth` API with `NodeId="All"`.
* Deleting the resource disables burst.
* The API may take 2-4 minutes to complete as the instance goes through `Changing` → `Normal` status.
* If applying this resource together with `st-alicloud_kvstore_per_shard_bandwidth` on the same instance, the provider retries on concurrent operation errors. Use `depends_on` for cleaner sequential ordering.
