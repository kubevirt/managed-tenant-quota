package util

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/managed-tenant-quota/pkg/apis/core/v1alpha1"
	v1alpha12 "kubevirt.io/managed-tenant-quota/pkg/generated/clientset/versioned/typed/core/v1alpha1"
	"time"
)

const launcherLabel = "virt-launcher"

func GetVirtCli() (kubecli.KubevirtClient, error) {
	clientConfig, err := kubecli.GetKubevirtClientConfig()
	if err != nil {
		return nil, err
	}

	virtCli, err := kubecli.GetKubevirtClientFromRESTConfig(clientConfig)
	if err != nil {
		return nil, err
	}

	return virtCli, err
}

func GetMTQCli() (v1alpha12.MtqV1alpha1Client, error) {
	cfg, err := kubecli.GetKubevirtClientConfig()
	if err != nil {
		klog.Fatalf("Unable to get kube config: %v\n", errors.WithStack(err))
	}
	MTQCli := v1alpha12.NewForConfigOrDie(cfg)
	return *MTQCli, nil
}

func GetMigrationInformer(virtCli kubecli.KubevirtClient) (cache.SharedIndexInformer, error) {
	listWatcher := NewListWatchFromClient(virtCli.RestClient(), "virtualmachineinstancemigrations", k8sv1.NamespaceAll, fields.Everything(), labels.Everything())
	migrationInformer := cache.NewSharedIndexInformer(listWatcher, &k6tv1.VirtualMachineInstanceMigration{}, 1*time.Hour, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	return migrationInformer, nil
}

func GetVirtualMachineMigrationResourceQuotaInformer(mtqCli v1alpha12.MtqV1alpha1Client) (cache.SharedIndexInformer, error) {
	listWatcher := NewListWatchFromClient(mtqCli.RESTClient(), "virtualmachinemigrationresourcequotas", k8sv1.NamespaceAll, fields.Everything(), labels.Everything())
	vmmrqInformer := cache.NewSharedIndexInformer(listWatcher, &v1alpha1.VirtualMachineMigrationResourceQuota{}, 1*time.Hour, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	return vmmrqInformer, nil
}

func GetLauncherPodInformer(virtCli kubecli.KubevirtClient) (cache.SharedIndexInformer, error) {
	labelSelector, err := labels.Parse(fmt.Sprintf(k6tv1.AppLabel+" in (%s)", launcherLabel))
	if err != nil {
		panic(err)
	}
	listWatcher := NewListWatchFromClient(virtCli.CoreV1().RESTClient(), "pods", k8sv1.NamespaceAll, fields.Everything(), labelSelector)
	podInformer := cache.NewSharedIndexInformer(listWatcher, &v1.Pod{}, 1*time.Hour, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	return podInformer, nil
}

func GetSecretInformer(virtCli kubecli.KubevirtClient, ns string) (cache.SharedIndexInformer, error) {
	listWatcher := NewListWatchFromClient(virtCli.CoreV1().RESTClient(), "secrets", ns, fields.Everything(), labels.Everything())
	secretInformer := cache.NewSharedIndexInformer(listWatcher, &v1.Secret{}, 1*time.Hour, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	return secretInformer, nil
}

func GetVMIInformer(virtCli kubecli.KubevirtClient) (cache.SharedIndexInformer, error) {
	listWatcher := NewListWatchFromClient(virtCli.RestClient(), "virtualmachineinstances", k8sv1.NamespaceAll, fields.Everything(), labels.Everything())
	vmiInformer := cache.NewSharedIndexInformer(listWatcher, &k6tv1.VirtualMachineInstance{}, 1*time.Hour, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	return vmiInformer, nil
}

func KubeVirtInformer(virtCli kubecli.KubevirtClient) (cache.SharedIndexInformer, error) {
	listWatcher := NewListWatchFromClient(virtCli.RestClient(), "kubevirts", k8sv1.NamespaceAll, fields.Everything(), labels.Everything())
	kubevirtInformer := cache.NewSharedIndexInformer(listWatcher, &k6tv1.KubeVirt{}, 1*time.Hour, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	return kubevirtInformer, nil
}
func GetResourceQuotaInformer(virtCli kubecli.KubevirtClient) (cache.SharedIndexInformer, error) {
	listWatcher := NewListWatchFromClient(virtCli.CoreV1().RESTClient(), "resourcequotas", k8sv1.NamespaceAll, fields.Everything(), labels.Everything())
	vmiInformer := cache.NewSharedIndexInformer(listWatcher, &v1.ResourceQuota{}, 1*time.Hour, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	return vmiInformer, nil
}

// NewListWatchFromClient creates a new ListWatch from the specified client, resource, kubevirtNamespace and field selector.
func NewListWatchFromClient(c cache.Getter, resource string, namespace string, fieldSelector fields.Selector, labelSelector labels.Selector) *cache.ListWatch {
	listFunc := func(options k8sv1.ListOptions) (runtime.Object, error) {
		options.FieldSelector = fieldSelector.String()
		options.LabelSelector = labelSelector.String()
		return c.Get().
			Namespace(namespace).
			Resource(resource).
			VersionedParams(&options, k8sv1.ParameterCodec).
			Do(context.Background()).
			Get()
	}
	watchFunc := func(options k8sv1.ListOptions) (watch.Interface, error) {
		options.FieldSelector = fieldSelector.String()
		options.LabelSelector = labelSelector.String()
		options.Watch = true
		return c.Get().
			Namespace(namespace).
			Resource(resource).
			VersionedParams(&options, k8sv1.ParameterCodec).
			Watch(context.Background())
	}
	return &cache.ListWatch{ListFunc: listFunc, WatchFunc: watchFunc}
}
