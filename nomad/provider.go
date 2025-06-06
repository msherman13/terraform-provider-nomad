// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package nomad

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/hashicorp/nomad/api"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type ProviderConfig struct {
	client *api.Client
	config *api.Config
}

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"address": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc("NOMAD_ADDR", nil),
				Description: "URL of the root of the target Nomad agent.",
			},
			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Region of the target Nomad agent.",
			},
			"http_auth": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("NOMAD_HTTP_AUTH", ""),
				Description: "HTTP basic auth configuration.",
			},
			"ca_file": {
				Type:          schema.TypeString,
				Optional:      true,
				DefaultFunc:   schema.EnvDefaultFunc("NOMAD_CACERT", nil),
				Description:   "A path to a PEM-encoded certificate authority used to verify the remote agent's certificate.",
				ConflictsWith: []string{"ca_pem"},
			},
			"ca_pem": {
				Type:          schema.TypeString,
				Optional:      true,
				Description:   "PEM-encoded certificate authority used to verify the remote agent's certificate.",
				ConflictsWith: []string{"ca_file"},
			},
			"cert_file": {
				Type:          schema.TypeString,
				Optional:      true,
				DefaultFunc:   schema.EnvDefaultFunc("NOMAD_CLIENT_CERT", nil),
				Description:   "A path to a PEM-encoded certificate provided to the remote agent; requires use of key_file or key_pem.",
				ConflictsWith: []string{"cert_pem"},
			},
			"cert_pem": {
				Type:          schema.TypeString,
				Optional:      true,
				Description:   "PEM-encoded certificate provided to the remote agent; requires use of key_file or key_pem.",
				ConflictsWith: []string{"cert_file"},
			},
			"key_file": {
				Type:          schema.TypeString,
				Optional:      true,
				DefaultFunc:   schema.EnvDefaultFunc("NOMAD_CLIENT_KEY", nil),
				Description:   "A path to a PEM-encoded private key, required if cert_file or cert_pem is specified.",
				ConflictsWith: []string{"key_pem"},
			},
			"key_pem": {
				Type:          schema.TypeString,
				Optional:      true,
				Description:   "PEM-encoded private key, required if cert_file or cert_pem is specified.",
				ConflictsWith: []string{"key_file"},
			},
			"secret_id": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("NOMAD_TOKEN", ""),
				Description: "ACL token secret for API requests.",
			},
			"headers": {
				Type:        schema.TypeList,
				Optional:    true,
				Sensitive:   true,
				Description: "The headers to send with each Nomad request.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "The header name",
						},
						"value": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "The header value",
						},
					},
				},
			},
			"ignore_env_vars": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "A set of environment variables that are ignored by the provider when configuring the Nomad API client.",
				Elem:        &schema.Schema{Type: schema.TypeBool},
			},
			"skip_verify": {
				Type:        schema.TypeBool,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("NOMAD_SKIP_VERIFY", false),
				Description: "Skip TLS verification on client side.",
			},
		},

		ConfigureFunc: providerConfigure,

		DataSourcesMap: map[string]*schema.Resource{
			"nomad_acl_policies":        dataSourceAclPolicies(),
			"nomad_acl_policy":          dataSourceAclPolicy(),
			"nomad_acl_role":            dataSourceACLRole(),
			"nomad_acl_roles":           dataSourceACLRoles(),
			"nomad_acl_token":           dataSourceACLToken(),
			"nomad_acl_tokens":          dataSourceACLTokens(),
			"nomad_allocations":         dataSourceAllocations(),
			"nomad_datacenters":         dataSourceDatacenters(),
			"nomad_deployments":         dataSourceDeployments(),
			"nomad_dynamic_host_volume": dataSourceDynamicHostVolume(),
			"nomad_job":                 dataSourceJob(),
			"nomad_job_parser":          dataSourceJobParser(),
			"nomad_jwks":                dataSourceJWKS(),
			"nomad_namespace":           dataSourceNamespace(),
			"nomad_namespaces":          dataSourceNamespaces(),
			"nomad_node_pool":           dataSourceNodePool(),
			"nomad_node_pools":          dataSourceNodePools(),
			"nomad_plugin":              dataSourcePlugin(),
			"nomad_plugins":             dataSourcePlugins(),
			"nomad_scaling_policies":    dataSourceScalingPolicies(),
			"nomad_scaling_policy":      dataSourceScalingPolicy(),
			"nomad_scheduler_config":    dataSourceSchedulerConfig(),
			"nomad_regions":             dataSourceRegions(),
			"nomad_volumes":             dataSourceVolumes(),
			"nomad_variable":            dataSourceVariable(),
		},

		ResourcesMap: map[string]*schema.Resource{
			"nomad_acl_auth_method":                  resourceACLAuthMethod(),
			"nomad_acl_binding_rule":                 resourceACLBindingRule(),
			"nomad_acl_policy":                       resourceACLPolicy(),
			"nomad_acl_role":                         resourceACLRole(),
			"nomad_acl_token":                        resourceACLToken(),
			"nomad_csi_volume":                       resourceCSIVolume(),
			"nomad_csi_volume_registration":          resourceCSIVolumeRegistration(),
			"nomad_dynamic_host_volume":              resourceDynamicHostVolume(),
			"nomad_dynamic_host_volume_registration": resourceDynamicHostVolumeRegistration(),
			"nomad_external_volume":                  resourceExternalVolume(),
			"nomad_job":                              resourceJob(),
			"nomad_namespace":                        resourceNamespace(),
			"nomad_node_pool":                        resourceNodePool(),
			"nomad_quota_specification":              resourceQuotaSpecification(),
			"nomad_sentinel_policy":                  resourceSentinelPolicy(),
			"nomad_volume":                           resourceVolume(),
			"nomad_scheduler_config":                 resourceSchedulerConfig(),
			"nomad_variable":                         resourceVariable(),
		},
	}
}

func providerConfigure(d *schema.ResourceData) (interface{}, error) {
	ignoreEnvVars := d.Get("ignore_env_vars").(map[string]interface{})
	if len(ignoreEnvVars) == 0 {
		// The Terraform SDK doesn't support DefaultFunc for complex types yet,
		// so implement the default value logic here for now.
		// https://github.com/hashicorp/terraform-plugin-sdk/issues/142
		if os.Getenv("TFC_RUN_ID") != "" {
			ignoreEnvVars = map[string]interface{}{
				"NOMAD_NAMESPACE": true,
				"NOMAD_REGION":    true,
			}
		}
	}

	conf := api.DefaultConfig()
	conf.Address = d.Get("address").(string)
	conf.SecretID = d.Get("secret_id").(string)

	if region, ok := d.GetOk("region"); ok {
		conf.Region = region.(string)
	} else if ignore, ok := ignoreEnvVars["NOMAD_REGION"]; ok && ignore.(bool) {
		conf.Region = ""
	}

	// The namespace is set per-resource but `DefaultConfig` loads it from the
	// NOMAD_NAMESPACE env var automatically. This will cause problems when
	// Terraform is running within a Nomad job (such as in Terraform Cloud) so
	// we need to unset it unless the provider is configured to load it.
	if ignore, ok := ignoreEnvVars["NOMAD_NAMESPACE"]; ok && ignore.(bool) {
		conf.Namespace = ""
	}

	// HTTP basic auth configuration.
	httpAuth := d.Get("http_auth").(string)
	if httpAuth != "" {
		var username, password string
		if strings.Contains(httpAuth, ":") {
			split := strings.SplitN(httpAuth, ":", 2)
			username = split[0]
			password = split[1]
		} else {
			username = httpAuth
		}
		conf.HttpAuth = &api.HttpBasicAuth{Username: username, Password: password}
	}

	// TLS configuration items.
	conf.TLSConfig.CACert = d.Get("ca_file").(string)
	conf.TLSConfig.ClientCert = d.Get("cert_file").(string)
	conf.TLSConfig.ClientKey = d.Get("key_file").(string)
	conf.TLSConfig.CACertPEM = []byte(d.Get("ca_pem").(string))
	conf.TLSConfig.ClientCertPEM = []byte(d.Get("cert_pem").(string))
	conf.TLSConfig.ClientKeyPEM = []byte(d.Get("key_pem").(string))
	conf.TLSConfig.Insecure = d.Get("skip_verify").(bool)

	if _, ok := os.LookupEnv("TF_ACC"); ok {
		// Revert the Nomad API client to non-pooled to avoid EOF errors when
		// running the test suite since it instantiates the provider multiple
		// times, creating several clients in parallel.
		// https://github.com/hashicorp/nomad/pull/12492
		conf.HttpClient = nonPooledHttpClient()
	}

	// Set headers if provided
	headers := d.Get("headers").([]interface{})
	parsedHeaders := make(http.Header)

	for _, h := range headers {
		header := h.(map[string]interface{})
		if name, ok := header["name"]; ok {
			parsedHeaders.Add(name.(string), header["value"].(string))
		}
	}
	conf.Headers = parsedHeaders

	client, err := api.NewClient(conf)
	if err != nil {
		return nil, fmt.Errorf("failed to configure Nomad API: %s", err)
	}

	res := ProviderConfig{
		config: conf,
		client: client,
	}

	return res, nil
}

func nonPooledHttpClient() *http.Client {
	httpClient := cleanhttp.DefaultClient()
	transport := httpClient.Transport.(*http.Transport)
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Default to http/1: alloc exec/websocket aren't supported in http/2
	// well yet: https://github.com/gorilla/websocket/issues/417
	transport.ForceAttemptHTTP2 = false

	return httpClient
}
