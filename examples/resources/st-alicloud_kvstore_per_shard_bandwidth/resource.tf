# Per-shard additional bandwidth
resource "st-alicloud_kvstore_per_shard_bandwidth" "shard_0" {
  instance_id = "r-xxxxx"
  shard_id    = "r-xxxxx-db-0"
  bandwidth   = 20
}
