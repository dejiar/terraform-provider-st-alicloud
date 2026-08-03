---
subcategory: "Redis (R-Kvstore)"
layout: "alicloud"
page_title: "ST-Alicloud: kvstore_individual_shard_bandwidth"
description: |-
  Manages additional individual shard bandwidth for an Alibaba Cloud Redis instance.
---

# st-alicloud_kvstore_individual_shard_bandwidth

Manages additional individual shard bandwidth for an Alibaba Cloud Redis (R-Kvstore) instance.

This purchases **permanent** additional bandwidth for a specific shard (node). Use `DescribeRoleZoneInfo` or `DescribeLogicInstanceTopology` to list available shard IDs.

## Example Usage

```hcl
resource "st-alicloud_kvstore_individual_shard_bandwidth" "shard_0" {
  instance_id = "r-xxxxx"
  shard_id    = "r-xxxxx-db-0"
  bandwidth   = 20
}
```

### With elastic burst (both resources on the same instance)

```hcl
resource "st-alicloud_kvstore_elastic_burst_bandwidth" "burst" {
  instance_id         = "r-xxxxx"
  burstable_bandwidth = true
}

resource "st-alicloud_kvstore_individual_shard_bandwidth" "shard_0" {
  instance_id = "r-xxxxx"
  shard_id    = "r-xxxxx-db-0"
  bandwidth   = 20

  depends_on = [st-alicloud_kvstore_elastic_burst_bandwidth.burst]
}
```

## Argument Reference

The following arguments are supported:

* `instance_id` - (Required, Forces new resource) The ID of the Redis instance.
* `shard_id` - (Required, Forces new resource) The shard (node) ID in InsName format (e.g. `r-xxxxx-db-0`). Use `DescribeRoleZoneInfo` or `DescribeLogicInstanceTopology` to list available shard IDs.
* `bandwidth` - (Required) **Total desired bandwidth** in MB/s for the shard. The provider reads `DefaultBandWidth` from the API and calculates the additional delta (`desired - default`). Must be greater than `DefaultBandWidth`. Setting it equal to `DefaultBandWidth` is an error (no additional bandwidth to purchase).

## Attribute Reference

The following attributes are exported:

* `id` - The resource ID. Format: `instance_id:shard_id`.

## Notes

* Per-shard bandwidth is permanent additional bandwidth purchased for a specific shard.
* This resource uses the `EnableAdditionalBandwidth` API with `NodeId` set to the shard's InsName.
* Deleting the resource resets the shard's bandwidth to default (0 additional).
* If the Redis instance is already destroyed, `Delete` is a no-op (no error).
* The provider waits for instance `Normal` status before and after the API call (up to 10 min before, 5 min after).
* `Task.Conflict` errors (unfinished task) are retried with exponential backoff up to 5 minutes.
* The API may take up to 5 minutes to complete as the instance goes through `Changing` → `Normal` status.
* If applying this resource together with `st-alicloud_kvstore_elastic_burst_bandwidth` on the same instance, add `depends_on = [st-alicloud_kvstore_elastic_burst_bandwidth.burst]` to this resource. Enabling burst wipes per-shard bandwidth, so per-shard must run **after** burst. The per-shard call naturally preserves burst on sibling shards — do NOT include `BandWidthBurst` in the API call (it breaks burst on siblings).
