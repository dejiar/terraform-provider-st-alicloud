---
subcategory: "Redis (R-Kvstore)"
layout: "alicloud"
page_title: "ST-Alicloud: kvstore_per_shard_bandwidth"
description: |-
  Manages additional per-shard bandwidth for an Alibaba Cloud Redis instance.
---

# st-alicloud_kvstore_per_shard_bandwidth

Manages additional per-shard bandwidth for an Alibaba Cloud Redis (R-Kvstore) instance.

This purchases **permanent** additional bandwidth for a specific shard (node). Use `DescribeRoleZoneInfo` or `DescribeLogicInstanceTopology` to list available shard IDs.

## Example Usage

```hcl
resource "st-alicloud_kvstore_per_shard_bandwidth" "shard_0" {
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

resource "st-alicloud_kvstore_per_shard_bandwidth" "shard_0" {
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
* `bandwidth` - (Required) Additional bandwidth in MB/s for the shard. Must be a positive integer (>= 1). The max per-shard additional bandwidth is `IntranetBandWidthBurst - DefaultBandWidth` (both read from the API). Validate this in your Terraform `variable` `validation` blocks.

## Attribute Reference

The following attributes are exported:

* `id` - The resource ID. Format: `instance_id:shard_id`.

## Import

Redis per-shard bandwidth can be imported using the format `instance_id:shard_id`:

```shell
terraform import st-alicloud_kvstore_per_shard_bandwidth.shard_0 r-xxxxx:r-xxxxx-db-0
```

## Notes

* Per-shard bandwidth is permanent additional bandwidth purchased for a specific shard.
* This resource uses the `EnableAdditionalBandwidth` API with `NodeId` set to the shard's InsName.
* Deleting the resource resets the shard's bandwidth to default (0 additional).
* The API may take 2-4 minutes to complete as the instance goes through `Changing` → `Normal` status.
* If applying this resource together with `st-alicloud_kvstore_elastic_burst_bandwidth` on the same instance, the provider retries on concurrent operation errors. Use `depends_on` for cleaner sequential ordering.
