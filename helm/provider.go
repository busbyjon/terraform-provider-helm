package helm

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/helmpath"
	"helm.sh/helm/v3/pkg/storage/driver"

	// Import to initialize client auth plugins.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

// Meta is the meta information structure for the provider
type Meta struct {
	data       *schema.ResourceData
	Settings   *cli.EnvSettings
	HelmDriver string

	// Used to lock some operations
	sync.Mutex
}

// Provider returns the provider schema to Terraform.
func Provider() *schema.Provider {
	p := &schema.Provider{
		Schema: map[string]*schema.Schema{
			"debug": {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Debug indicates whether or not Helm is running in Debug mode.",
				DefaultFunc: schema.EnvDefaultFunc("HELM_DEBUG", false),
			},
			"plugins_path": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The path to the helm plugins directory",
				DefaultFunc: schema.EnvDefaultFunc("HELM_PLUGINS", helmpath.DataPath("plugins")),
			},
			"registry_config_path": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The path to the registry config file",
				DefaultFunc: schema.EnvDefaultFunc("HELM_REGISTRY_CONFIG", helmpath.ConfigPath("registry.json")),
			},
			"repository_config_path": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The path to the file containing repository names and URLs",
				DefaultFunc: schema.EnvDefaultFunc("HELM_REPOSITORY_CONFIG", helmpath.ConfigPath("repositories.yaml")),
			},
			"repository_cache": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The path to the file containing cached repository indexes",
				DefaultFunc: schema.EnvDefaultFunc("HELM_REPOSITORY_CACHE", helmpath.CachePath("repository")),
			},
			"helm_driver": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The backend storage driver. Values are: configmap, secret, memory, sql",
				DefaultFunc: schema.EnvDefaultFunc("HELM_DRIVER", "secret"),
				ValidateDiagFunc: func(val interface{}, key cty.Path) (diags diag.Diagnostics) {
					drivers := []string{
						strings.ToLower(driver.MemoryDriverName),
						strings.ToLower(driver.ConfigMapsDriverName),
						strings.ToLower(driver.SecretsDriverName),
						strings.ToLower(driver.SQLDriverName),
					}

					v := strings.ToLower(val.(string))

					for _, d := range drivers {
						if d == v {
							return
						}
					}
					return diag.Diagnostics{
						{
							Severity: diag.Error,
							Summary:  fmt.Sprintf("Invalid storage driver: %v used for helm_driver", v),
							Detail:   fmt.Sprintf("Helm backend storage driver must be set to one of the following values: %v", strings.Join(drivers, ", ")),
						},
					}
				},
			},
			"kubernetes": {
				Type:        schema.TypeList,
				MaxItems:    1,
				Optional:    true,
				Description: "Kubernetes configuration.",
				Elem:        kubernetesResource(),
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"helm_release": resourceRelease(),
		},
	}
	p.ConfigureContextFunc = func(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
		return providerConfigure(d, p.TerraformVersion)
	}
	return p
}

func kubernetesResource() *schema.Resource {
	return &schema.Resource{
		Schema: map[string]*schema.Schema{
			"host": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_HOST", ""),
				Description: "The hostname (in form of URI) of Kubernetes master.",
			},
			"username": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_USER", ""),
				Description: "The username to use for HTTP basic authentication when accessing the Kubernetes master endpoint.",
			},
			"password": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_PASSWORD", ""),
				Description: "The password to use for HTTP basic authentication when accessing the Kubernetes master endpoint.",
			},
			"insecure": {
				Type:        schema.TypeBool,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_INSECURE", false),
				Description: "Whether server should be accessed without verifying the TLS certificate.",
			},
			"client_certificate": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_CLIENT_CERT_DATA", ""),
				Description: "PEM-encoded client certificate for TLS authentication.",
			},
			"client_key": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_CLIENT_KEY_DATA", ""),
				Description: "PEM-encoded client certificate key for TLS authentication.",
			},
			"cluster_ca_certificate": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_CLUSTER_CA_CERT_DATA", ""),
				Description: "PEM-encoded root certificates bundle for TLS authentication.",
			},
			"config_paths": {
				Type:        schema.TypeList,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Optional:    true,
				Description: "A list of paths to kube config files. Can be set with KUBE_CONFIG_PATHS environment variable.",
			},
			"config_path": {
				Type:          schema.TypeString,
				Optional:      true,
				DefaultFunc:   schema.EnvDefaultFunc("KUBE_CONFIG_PATH", nil),
				Description:   "Path to the kube config file. Can be set with KUBE_CONFIG_PATH.",
				ConflictsWith: []string{"kubernetes.0.config_paths"},
			},
			"config_context": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_CTX", ""),
			},
			"config_context_auth_info": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_CTX_AUTH_INFO", ""),
				Description: "",
			},
			"config_context_cluster": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_CTX_CLUSTER", ""),
				Description: "",
			},
			"token": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("KUBE_TOKEN", ""),
				Description: "Token to authenticate an service account",
			},
			"exec": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"api_version": {
							Type:     schema.TypeString,
							Required: true,
						},
						"command": {
							Type:     schema.TypeString,
							Required: true,
						},
						"env": {
							Type:     schema.TypeMap,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"args": {
							Type:     schema.TypeList,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
					},
				},
				Description: "",
			},
		},
	}
}

var apiTokenMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"

func inCluster() bool {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return false
	}

	if _, err := os.Stat(apiTokenMountPath); err != nil {
		return false
	}
	return true
}

var authDocumentationURL = "https://registry.terraform.io/providers/hashicorp/helm/latest/docs#authentication"

func checkKubernetesConfigurationValid(d *schema.ResourceData) error {
	if inCluster() {
		log.Printf("[DEBUG] Terraform appears to be running inside the Kubernetes cluster")
		return nil
	}

	if os.Getenv("KUBE_CONFIG_PATHS") != "" {
		return nil
	}

	atLeastOneOf := []string{
		"host",
		"config_path",
		"config_paths",
		"client_certificate",
		"token",
		"exec",
	}
	for _, a := range atLeastOneOf {
		if _, ok := k8sGetOk(d, a); ok {
			return nil
		}
	}

	return fmt.Errorf(`provider not configured: you must configure a path to your kubeconfig
or explicitly supply credentials via the provider block or environment variables.

See our documentation at: %s`, authDocumentationURL)
}

func providerConfigure(d *schema.ResourceData, terraformVersion string) (interface{}, diag.Diagnostics) {
	m := &Meta{data: d}

	if err := checkKubernetesConfigurationValid(d); err != nil {
		return nil, diag.FromErr(err)
	}

	settings := cli.New()
	settings.Debug = d.Get("debug").(bool)

	if v, ok := d.GetOk("plugins_path"); ok {
		settings.PluginsDirectory = v.(string)
	}

	if v, ok := d.GetOk("registry_config_path"); ok {
		settings.RegistryConfig = v.(string)
	}

	if v, ok := d.GetOk("repository_config_path"); ok {
		settings.RepositoryConfig = v.(string)
	}

	if v, ok := d.GetOk("repository_cache"); ok {
		settings.RepositoryCache = v.(string)
	}

	m.Settings = settings

	if v, ok := d.GetOk("helm_driver"); ok {
		m.HelmDriver = v.(string)
	}

	return m, nil
}

var k8sPrefix = "kubernetes.0."

func k8sGetOk(d *schema.ResourceData, key string) (interface{}, bool) {
	value, ok := d.GetOk(k8sPrefix + key)

	// For boolean attributes the zero value is Ok
	switch value.(type) {
	case bool:
		// TODO: replace deprecated GetOkExists with SDK v2 equivalent
		// https://github.com/hashicorp/terraform-plugin-sdk/pull/350
		value, ok = d.GetOkExists(k8sPrefix + key)
	}

	// fix: DefaultFunc is not being triggered on TypeList
	s := kubernetesResource().Schema[key]
	if !ok && s.DefaultFunc != nil {
		value, _ = s.DefaultFunc()

		switch v := value.(type) {
		case string:
			ok = len(v) != 0
		case bool:
			ok = v
		}
	}

	return value, ok
}

func k8sGet(d *schema.ResourceData, key string) interface{} {
	value, _ := k8sGetOk(d, key)
	return value
}

func expandStringSlice(s []interface{}) []string {
	result := make([]string, len(s), len(s))
	for k, v := range s {
		// Handle the Terraform parser bug which turns empty strings in lists to nil.
		if v == nil {
			result[k] = ""
		} else {
			result[k] = v.(string)
		}
	}
	return result
}

// GetHelmConfiguration will return a new Helm configuration
func (m *Meta) GetHelmConfiguration(namespace string) (*action.Configuration, error) {
	m.Lock()
	defer m.Unlock()
	debug("[INFO] GetHelmConfiguration start")
	actionConfig := new(action.Configuration)

	kc, err := newKubeConfig(m.data, &namespace)
	if err != nil {
		return nil, err
	}

	if err := actionConfig.Init(kc, namespace, m.HelmDriver, debug); err != nil {
		return nil, err
	}
	debug("[INFO] GetHelmConfiguration success")
	return actionConfig, nil
}

func debug(format string, a ...interface{}) {
	log.Printf("[DEBUG] %s", fmt.Sprintf(format, a...))
}
