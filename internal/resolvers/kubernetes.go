package resolvers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"time"

	"github.com/davejbax/tailscale-dns-proxy/internal/iplist"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	indexByServicePath = "IndexByServicePath"
	indexByExternalIp  = "IndexByExternalIp"

	labelTailscaleParentResource     = "tailscale.com/parent-resource"
	labelTailscaleParentResourceNs   = "tailscale.com/parent-resource-ns"
	labelTailscaleParentResourceType = "tailscale.com/parent-resource-type"

	// Key in tailscale-operator Secrets' data for device IPs
	tailscaleSecretDataDeviceIps = "device_ips"

	typeService = "svc"
)

func makeServicePath(namespace string, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

type KubernetesConfig struct {
	InformerResyncPeriodSeconds int    `mapstructure:"informer_resync_period_seconds"`
	TailscaleOperatorNamespace  string `mapstructure:"tailscale_operator_namespace"`
}

// KubernetesResolver is a [Resolver] that resolves Tailscale IPs from external
// IPs by peeking at internal state of the tailscale-operator. This resolver is
// able to map Services with an External (ingress) IP to the corresponding
// Tailscale IP, provided the Service is exposed by the tailscale-operator.
//
// Note that this resolver must first be started before use with
// [KubernetesResolver.StartAndWaitForCacheSync].
// TODO implement self resolver func
type KubernetesResolver struct {
	serviceFactory  informers.SharedInformerFactory
	secretInformer  cache.SharedIndexInformer
	secretFactory   informers.SharedInformerFactory
	serviceInformer cache.SharedIndexInformer
}

func NewKubernetesResolverWithDefaultClient(config *KubernetesConfig) (*KubernetesResolver, error) {
	// Try the in-cluster config first: this throws an error if we're not in the cluster,
	// at which point we'll try loading the kubeconfig from default locations
	// instead (user's home directory etc.)
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		if !errors.Is(err, rest.ErrNotInCluster) {
			return nil, fmt.Errorf("failed to create in-cluster kubeconfig: %w", err)
		}

		// We're not in a cluster: try loading kubeconfig from default locations
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		)

		kubeConfig, err = clientConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("not in cluster and failed to load kubeconfig from default out-of-cluster locations: %w", err)
		}
	}

	kube, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return NewKubernetesResolverFromConfig(kube, config), nil
}

func NewKubernetesResolverFromConfig(client kubernetes.Interface, config *KubernetesConfig) *KubernetesResolver {
	return NewKubernetesResolver(client, time.Duration(config.InformerResyncPeriodSeconds)*time.Second, config.TailscaleOperatorNamespace)
}

func NewKubernetesResolver(client kubernetes.Interface, resync time.Duration, tailscaleOperatorNamespace string) *KubernetesResolver {
	registry := &KubernetesResolver{}

	registry.secretFactory = informers.NewSharedInformerFactoryWithOptions(client, resync,
		informers.WithNamespace(tailscaleOperatorNamespace),
	)
	registry.secretInformer = registry.secretFactory.Core().V1().Secrets().Informer()

	registry.secretInformer.AddIndexers(map[string]cache.IndexFunc{
		indexByServicePath: func(obj interface{}) ([]string, error) {
			secret := obj.(*corev1.Secret)

			parentResource, ok := secret.Labels[labelTailscaleParentResource]
			if !ok {
				return nil, nil
			}

			parentResourceNs, ok := secret.Labels[labelTailscaleParentResourceNs]
			if !ok {
				return nil, nil
			}

			parentResourceType, ok := secret.Labels[labelTailscaleParentResourceType]
			if !ok || parentResourceType != typeService {
				return nil, nil
			}

			return []string{makeServicePath(parentResourceNs, parentResource)}, nil
		},
	})

	registry.serviceFactory = informers.NewSharedInformerFactory(client, resync)
	registry.serviceInformer = registry.serviceFactory.Core().V1().Services().Informer()

	registry.serviceInformer.AddIndexers(map[string]cache.IndexFunc{
		indexByExternalIp: func(obj interface{}) ([]string, error) {
			service := obj.(*corev1.Service)

			var ips []string
			for _, ingress := range service.Status.LoadBalancer.Ingress {
				if ingress.IP != "" {
					ips = append(ips, ingress.IP)
				}
			}

			return ips, nil
		},
	})

	return registry
}

func startAndWaitForCacheSync(factory informers.SharedInformerFactory, cancel <-chan struct{}) error {
	factory.Start(cancel)

	var syncErrors []error
	for cache, ok := range factory.WaitForCacheSync(cancel) {
		if !ok {
			syncErrors = append(syncErrors, &CacheSyncError{cache: cache})
		}
	}

	return errors.Join(syncErrors...)
}

func (r *KubernetesResolver) Start(cancel <-chan struct{}) error {
	return errors.Join(
		startAndWaitForCacheSync(r.secretFactory, cancel),
		startAndWaitForCacheSync(r.serviceFactory, cancel),
	)
}

func (r *KubernetesResolver) GetTailscaleIPsByService(serviceNamespace string, serviceName string) ([]string, error) {
	secrets, err := r.secretInformer.GetIndexer().ByIndex(indexByServicePath, makeServicePath(serviceNamespace, serviceName))
	if err != nil {
		return nil, fmt.Errorf("failed to query secret informer index: %w", err)
	}

	for _, secretI := range secrets {
		secret := secretI.(*corev1.Secret)
		ipsJson, ok := secret.Data[tailscaleSecretDataDeviceIps]
		if !ok {
			// This secret doesn't have the device_ips key. This could be because it's
			// not the secret we're looking for (unlikely), or because the corresponding
			// tailscale pod hasn't come up yet
			continue
		}

		var ips []string
		if err := json.Unmarshal(ipsJson, &ips); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tailscale-operator secret device IPs data: %w", err)
		}

		// XXX: We assume that there will only ever be one secret referring to this service here. I think
		// that makes sense with the operator currently: there is only one replica of the tailscale pod
		// in the replicaset, however that might change in future!
		if len(ips) > 0 {
			return ips, nil
		}
	}

	return nil, nil
}

func (r *KubernetesResolver) GetTailscaleIPsByExternalIP(externalIP net.IP) ([]net.IP, error) {
	services, err := r.serviceInformer.GetIndexer().ByIndex(indexByExternalIp, externalIP.String())
	if err != nil {
		return nil, fmt.Errorf("failed to query service informer index: %w", err)
	}

	for _, serviceI := range services {
		service := serviceI.(*corev1.Service)
		ips, err := r.GetTailscaleIPsByService(service.Namespace, service.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get tailscale IPs for service '%s/%s': %w", service.Namespace, service.Name, err)
		} else if len(ips) > 0 {
			return iplist.ParseIPs(ips)
		}
	}

	return nil, nil
}

type CacheSyncError struct {
	cache reflect.Type
}

func (c *CacheSyncError) Error() string {
	return fmt.Sprintf("failed to sync informer cache of type '%v'", c.cache)
}
