package utils

import "github.com/hashicorp/terraform-plugin-framework/types"

type ClientConfig struct {
	Region    types.String `tfsdk:"region"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
}

type ClientConfigWithZone struct {
	Region    types.String `tfsdk:"region"`
	Zone      types.String `tfsdk:"zone"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
}

func (cfg *ClientConfigWithZone) GetClientConfig() *ClientConfig {
	return &ClientConfig{
		Region:    cfg.Region,
		AccessKey: cfg.AccessKey,
		SecretKey: cfg.SecretKey,
	}
}
