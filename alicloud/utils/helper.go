package utils

import (
	"encoding/json"
	"strings"
	"time"

	alicloudOpenapiClient "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Convert the result for an array and returns a Json string
func ConvertListStringToJsonString(configured []string) string {
	if len(configured) < 1 {
		return ""
	}
	result := "["
	for i, v := range configured {
		if v == "" {
			continue
		}
		result += "\"" + v + "\""
		if i < len(configured)-1 {
			result += ","
		}
	}
	result += "]"
	return result
}

func ConvertJsonStringToListString(configured string) ([]string, error) {
	result := make([]string, 0)
	if err := json.Unmarshal([]byte(configured), &result); err != nil {
		return nil, err
	}

	return result, nil
}

func TrimStringQuotes(input string) string {
	return strings.TrimPrefix(strings.TrimSuffix(input, "\""), "\"")
}

// GetTimeout parses a configured timeout string (e.g. "30m") and falls back
// to defaultTimeout when the value is null, unknown, empty, or invalid.
func GetTimeout(value types.String, defaultTimeout time.Duration) time.Duration {
	if value.IsNull() || value.IsUnknown() || value.ValueString() == "" {
		return defaultTimeout
	}
	duration, err := time.ParseDuration(value.ValueString())
	if err != nil {
		return defaultTimeout
	}
	return duration
}

func InitNewClient(providerConfig *alicloudOpenapiClient.Client, planConfig *ClientConfig) (initClient bool, ClientConfig *alicloudOpenapiClient.Config, diag diag.Diagnostics) {
	initClient = false
	ClientConfig = &alicloudOpenapiClient.Config{}
	region := planConfig.Region.ValueString()
	accessKey := planConfig.AccessKey.ValueString()
	secretKey := planConfig.SecretKey.ValueString()

	if region != "" || accessKey != "" || secretKey != "" {
		initClient = true
	}

	if initClient {
		if region == "" {
			region = tea.StringValue(providerConfig.RegionId)
		}
		if accessKey == "" {
			clientAccessKey, err := providerConfig.Credential.GetAccessKeyId()
			if err != nil {
				diag.AddError(
					"Failed to retrieve client Access Key.",
					"This is an error in provider, please contact the provider developers.\n\n"+
						"Error: "+err.Error(),
				)
			} else {
				accessKey = tea.StringValue(clientAccessKey)
			}
		}
		if secretKey == "" {
			clientSecretKey, err := providerConfig.Credential.GetAccessKeySecret()
			if err != nil {
				diag.AddError(
					"Failed to retrieve client Secret Key.",
					"This is an error in provider, please contact the provider developers.\n\n"+
						"Error: "+err.Error(),
				)
			} else {
				secretKey = tea.StringValue(clientSecretKey)
			}
		}
		if diag.HasError() {
			return
		}

		ClientConfig = &alicloudOpenapiClient.Config{
			RegionId:        &region,
			AccessKeyId:     &accessKey,
			AccessKeySecret: &secretKey,
		}
	}

	return
}
