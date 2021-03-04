package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd/api"
)

type ExtractorOptions struct {
	configFlags *genericclioptions.ConfigFlags

	genericclioptions.IOStreams
}

// NewNamespaceOptions provides an instance of ExtractorOptions with default values
func NewExtractorOptions(streams genericclioptions.IOStreams) *ExtractorOptions {
	return &ExtractorOptions{
		configFlags: genericclioptions.NewConfigFlags(true),

		IOStreams: streams,
	}
}

// groupResource contains the APIGroup and APIResource
type groupResource struct {
	APIGroup        string
	APIVersion      string
	APIGroupVersion string
	APIResource     metav1.APIResource
}

type sortableResource struct {
	resources []groupResource
	sortBy    string
}

func (s sortableResource) Len() int { return len(s.resources) }
func (s sortableResource) Swap(i, j int) {
	s.resources[i], s.resources[j] = s.resources[j], s.resources[i]
}
func (s sortableResource) Less(i, j int) bool {
	ret := strings.Compare(s.compareValues(i, j))
	if ret > 0 {
		return false
	} else if ret == 0 {
		return strings.Compare(s.resources[i].APIResource.Name, s.resources[j].APIResource.Name) < 0
	}
	return true
}

func (s sortableResource) compareValues(i, j int) (string, string) {
	switch s.sortBy {
	case "name":
		return s.resources[i].APIResource.Name, s.resources[j].APIResource.Name
	case "kind":
		return s.resources[i].APIResource.Kind, s.resources[j].APIResource.Kind
	}
	return s.resources[i].APIGroup, s.resources[j].APIGroup
}

func main() {

	e := NewExtractorOptions(genericclioptions.IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	})

	config := e.configFlags.ToRawKubeConfigLoader()
	rawConfig, err := config.RawConfig()
	if err != nil {
		fmt.Printf("error in generating raw config")
		os.Exit(1)
	}
	context := rawConfig.CurrentContext

	if context == "" {
		fmt.Printf("current context is empty")
		os.Exit(1)
	}

	var currentContext *api.Context

	for name, ctx := range rawConfig.Contexts {
		if name == context {
			currentContext = ctx
		}
	}

	if currentContext == nil {
		fmt.Printf("currentContext is nil")
		os.Exit(1)
	}

	if len(currentContext.Namespace) == 0 {
		fmt.Printf("currentContext Namespace is empty ")
		os.Exit(1)
	}

	fmt.Printf("namespace of current context is: %s\n", currentContext.Namespace)

	//clientConfig, err := config.ClientConfig()
	//if err != nil {
	//	fmt.Printf("error getting client config: %#v", err)
	//	os.Exit(1)
	//}
	//
	//b := resource.NewBuilder(e.configFlags)

	discoveryclient, err := e.configFlags.ToDiscoveryClient()
	if err != nil {
		fmt.Printf("cannot create discovery client: %#v", err)
		os.Exit(1)
	}

	// Always request fresh data from the server
	discoveryclient.Invalidate()

	restConfig, err := e.configFlags.ToRESTConfig()
	if err != nil {
		fmt.Printf("cannot create rest config: %#v", err)
	}

	dynamicClient := dynamic.NewForConfigOrDie(restConfig)

	errs := []error{}
	lists, err := discoveryclient.ServerPreferredResources()
	if err != nil {
		errs = append(errs, err)
	}

	resources := []groupResource{}
	var errors []error

	for _, list := range lists {
		if len(list.APIResources) == 0 {
			continue
		}
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, resource := range list.APIResources {
			if len(resource.Verbs) == 0 {
				continue
			}

			if !resource.Namespaced {
				fmt.Printf("resource: %s.%s is clusterscoped, skipping\n", gv.String(), resource.Kind)
				continue
			}

			fmt.Printf("processing resource: %s.%s\n", gv.String(), resource.Kind)

			g := groupResource{
				APIGroup:        gv.Group,
				APIVersion:      gv.Version,
				APIGroupVersion: gv.String(),
				APIResource:     resource,
			}

			objs, err := getObjects(g, currentContext.Namespace, dynamicClient)
			if err != nil {
				switch {
				case apierrors.IsForbidden(err):
					fmt.Printf("cannot list obj in namespace")
				case apierrors.IsMethodNotSupported(err):
					fmt.Printf("list method not supported on the gvr")
				case apierrors.IsNotFound(err):
					fmt.Printf("could not find the resource, most likely this is a virtual resource")
				default:
					fmt.Printf("error listing objects: %#v", err)
					errors = append(errors, err)

				}
				continue
			}

			if len(objs.Items) > 0 {
				fmt.Printf("more than one object found\n")
				resources = append(resources, g)
				continue
			}

			fmt.Printf("0 objects found, skipping\n")
		}
	}

	sort.Stable(sortableResource{resources, "kind"})

	fmt.Printf("\nGVK's to be backed up\n\n")

	for _, r := range resources {
		fmt.Printf("%s\n", r.APIResource.Name+r.APIGroupVersion)
	}
}

func getObjects(g groupResource, namespace string, d dynamic.Interface) (*unstructured.UnstructuredList, error) {
	c := d.Resource(schema.GroupVersionResource{
		Group:    g.APIGroup,
		Version:  g.APIVersion,
		Resource: g.APIResource.Name,
	})
	return c.Namespace(namespace).List(context.Background(), metav1.ListOptions{})
}
