//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.

package main

import (
	"flag"
	"k8s.io/klog/v2"
	mtqnamespaced "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/namespaced"
	mtqoperator "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/operator"
	"kubevirt.io/managed-tenant-quota/tools/util"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"text/template"
)

type templateData struct {
	DockerRepo             string
	DockerTag              string
	OperatorVersion        string
	DeployClusterResources string
	OperatorImage          string
	ControllerImage        string
	MTQLockServerImage     string
	ClonerImage            string
	APIServerImage         string
	UploadProxyImage       string
	UploadServerImage      string
	Verbosity              string
	PullPolicy             string
	CrName                 string
	Namespace              string
	GeneratedManifests     map[string]string
}

var (
	dockerRepo             = flag.String("docker-repo", "", "")
	dockertag              = flag.String("docker-tag", "", "")
	operatorVersion        = flag.String("operator-version", "", "")
	genManifestsPath       = flag.String("generated-manifests-path", "", "")
	deployClusterResources = flag.String("deploy-cluster-resources", "", "")
	operatorImage          = flag.String("operator-image", "", "")
	controllerImage        = flag.String("controller-image", "", "")
	mtqLockServerImage     = flag.String("mtq-lock-server-image", "", "")
	verbosity              = flag.String("verbosity", "1", "")
	pullPolicy             = flag.String("pull-policy", "", "")
	crName                 = flag.String("cr-name", "", "")
	namespace              = flag.String("namespace", "", "")
)

func main() {
	templFile := flag.String("template", "", "")
	resourceType := flag.String("resource-type", "", "")
	resourceGroup := flag.String("resource-group", "everything", "")
	flag.Parse()

	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	flag.CommandLine.VisitAll(func(f1 *flag.Flag) {
		f2 := klogFlags.Lookup(f1.Name)
		if f2 != nil {
			value := f1.Value.String()
			if err := f2.Value.Set(value); err != nil {
				panic(err)
			}
		}
	})

	if *templFile != "" {
		generateFromFile(*templFile)
		return
	}

	generateFromCode(*resourceType, *resourceGroup)
}

func generateFromFile(templFile string) {
	data := &templateData{
		Verbosity:              *verbosity,
		DockerRepo:             *dockerRepo,
		DockerTag:              *dockertag,
		DeployClusterResources: *deployClusterResources,
		OperatorImage:          *operatorImage,
		ControllerImage:        *controllerImage,
		MTQLockServerImage:     *mtqLockServerImage,
		PullPolicy:             *pullPolicy,
		CrName:                 *crName,
		Namespace:              *namespace,
	}

	file, err := os.Open(templFile)
	if err != nil {
		klog.Fatalf("Failed to open file %s: %v", templFile, err)
	}
	defer file.Close()

	// Read generated manifests and populate templated manifest
	genDir := *genManifestsPath
	data.GeneratedManifests = make(map[string]string)
	manifests, err := os.ReadDir(genDir)
	if err != nil {
		klog.Fatalf("Failed to read directory %s: %v", genDir, err)
	}

	for _, manifest := range manifests {
		if manifest.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(genDir, manifest.Name()))
		if err != nil {
			klog.Fatalf("Failed to read file %s: %v", templFile, err)
		}

		data.GeneratedManifests[manifest.Name()] = string(b)
	}

	tmpl := template.Must(template.ParseFiles(templFile))
	err = tmpl.Execute(os.Stdout, data)
	if err != nil {
		klog.Fatalf("Error executing template: %v", err)
	}
}

type resourceGetter func(string) ([]client.Object, error)

var resourceGetterMap = map[string]resourceGetter{
	"namespaced": getNamespacedResources,
	"operator":   getOperatorResources,
}

func generateFromCode(resourceType, resourceGroup string) {
	f, ok := resourceGetterMap[resourceType]
	if !ok {
		klog.Fatalf("Unknown resource type %s", resourceType)
	}

	resources, err := f(resourceGroup)
	if err != nil {
		klog.Fatalf("Error getting resources: %v", err)
	}

	for _, resource := range resources {
		err := util.MarshallObject(resource, os.Stdout)
		if err != nil {
			klog.Fatalf("Error marshalling resource: %v", err)
		}
	}
}

func getOperatorResources(resourceGroup string) ([]client.Object, error) {
	args := &mtqoperator.FactoryArgs{
		NamespacedArgs: mtqnamespaced.FactoryArgs{
			Verbosity:              *verbosity,
			OperatorVersion:        *operatorVersion,
			DeployClusterResources: *deployClusterResources,
			ControllerImage:        *controllerImage,
			MTQLockServerImage:     *mtqLockServerImage,
			PullPolicy:             *pullPolicy,
			Namespace:              *namespace,
		},
		Image: *operatorImage,
	}

	return mtqoperator.CreateOperatorResourceGroup(resourceGroup, args)
}

func getNamespacedResources(codeGroup string) ([]client.Object, error) {
	args := &mtqnamespaced.FactoryArgs{
		Verbosity:          *verbosity,
		OperatorVersion:    *operatorVersion,
		ControllerImage:    *controllerImage,
		MTQLockServerImage: *mtqLockServerImage,
		PullPolicy:         *pullPolicy,
		Namespace:          *namespace,
	}

	return mtqnamespaced.CreateResourceGroup(codeGroup, args)
}
