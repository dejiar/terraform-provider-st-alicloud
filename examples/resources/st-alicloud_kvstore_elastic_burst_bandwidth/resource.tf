# Elastic burst bandwidth (instance-level)
resource "st-alicloud_kvstore_elastic_burst_bandwidth" "burst" {
  instance_id         = "r-xxxxx"
  burstable_bandwidth = true
}
