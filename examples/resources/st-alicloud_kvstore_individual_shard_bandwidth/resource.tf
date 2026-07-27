resource "st-alicloud_kvstore_elastic_burst_bandwidth" "burst" {
  instance_id         = "r-xxxxx"
  burstable_bandwidth = true
}

resource "st-alicloud_kvstore_individual_shard_bandwidth" "shard_0" {
  instance_id = "r-xxxxx"
  shard_id    = "r-xxxxx-db-0"
  bandwidth   = 20

  # Burst wipes per-shard bandwidth on enable.
  # Per-shard must run AFTER burst. It preserves burst on sibling shards.
  depends_on = [st-alicloud_kvstore_elastic_burst_bandwidth.burst]
}
