package main

import (
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	api "k8s.io/client-go/pkg/api/v1"
	batchv1 "k8s.io/client-go/pkg/apis/batch/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var VERSION = "v0.1.0"

var clientset *kubernetes.Clientset

var controller cache.Controller
var store cache.Store

func main() {
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config",
			Usage: "Kube config path for outside of cluster access",
		},
	}

	app.Action = func(c *cli.Context) error {
		var err error
		clientset, err = getClient(c.String("/home/matyix/.kube/config"))
		if err != nil {
			logrus.Error(err)
			return err
		}
		go pollNodes()
		watchNodes()
		watchPods()
		for {
			time.Sleep(5 * time.Second)
		}
	}
	app.Run(os.Args)
}

func listJobs() {

	batchClient := clientset.BatchV1Client
	jobsClient := batchClient.Jobs("default")

	jobsList, _ := jobsClient.List(v1.ListOptions{})

	for i, job := range jobsList.Items {
		logrus.Infof("Job %d: %s\n", i, job.Name)
	}

}

func createJobs() {

	falseVal := false

	batchClient := clientset.BatchV1Client
	jobsClient := batchClient.Jobs("default")

	batchJob := &batchv1.Job{
		TypeMeta: v1.TypeMeta{
			Kind:       "Job",
			APIVersion: "v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:   "testjob",
			Labels: make(map[string]string),
		},
		Spec: batchv1.JobSpec{
			// Optional: Parallelism:,
			// Optional: Completions:,
			// Optional: ActiveDeadlineSeconds:,
			// Optional: Selector:,
			// Optional: ManualSelector:,
			Template: api.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Name:   "testpod",
					Labels: make(map[string]string),
				},
				Spec: api.PodSpec{
					InitContainers: []api.Container{}, // Doesn't seem obligatory(?)...
					Containers: []api.Container{
						{
							Name:    "testing",
							Image:   "nginx",
							Command: []string{"sleep", "10"},
							SecurityContext: &api.SecurityContext{
								Privileged: &falseVal,
							},
							ImagePullPolicy: api.PullPolicy(api.PullIfNotPresent),
							Env:             []api.EnvVar{},
							VolumeMounts:    []api.VolumeMount{},
						},
					},
					RestartPolicy:    api.RestartPolicyOnFailure,
					Volumes:          []api.Volume{},
					ImagePullSecrets: []api.LocalObjectReference{},
				},
			},
		},
		// Optional, not used by pach: JobStatus:,
	}

	newJob, _ := jobsClient.Create(batchJob)

	logrus.Infof("Job %v", newJob.Name)

}

func deleteJob(name string) {

	batchClient := clientset.BatchV1Client
	jobsClient := batchClient.Jobs("default")

	deleteOptions := v1.DeleteOptions{}

	gracePeriodSeconds := int64(10)
	deleteOptions.GracePeriodSeconds = &gracePeriodSeconds

	jobsClient.Delete(name, &deleteOptions)

}
func watchNodes() {
	watchList := cache.NewListWatchFromClient(clientset.CoreV1().RESTClient(), "nodes", v1.NamespaceAll,
		fields.Everything())
	store, controller = cache.NewInformer(
		watchList,
		&api.Node{},
		time.Second*30,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    handleNodeAdd,
			UpdateFunc: handleNodeUpdate,
		},
	)

	stop := make(chan struct{})
	go controller.Run(stop)

	informer := cache.NewSharedIndexInformer(
		watchList,
		&api.Node{},
		time.Second*10,
		cache.Indexers{},
	)

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    handleNodeAdd,
		UpdateFunc: handleNodeUpdate,
	})
}

func watchPods() {
	watchList := cache.NewListWatchFromClient(clientset.CoreV1().RESTClient(), "pods", v1.NamespaceAll,
		fields.Everything())
	store, controller = cache.NewInformer(
		watchList,
		&api.Pod{},
		time.Second*30,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    podCreated,
			DeleteFunc: podDeleted,
		},
	)

	stop := make(chan struct{})
	go controller.Run(stop)

	informer := cache.NewSharedIndexInformer(
		watchList,
		&api.Pod{},
		time.Second*10,
		cache.Indexers{},
	)

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    podCreated,
		DeleteFunc: podDeleted,
	})

}

func handleNodeAdd(obj interface{}) {
	node := obj.(*api.Node)
	logrus.Infof("Node [%s] is added; checking resources...", node.Name)
	logrus.Infof("Node [%s] memory pressure state is [%v]", node.Name, isNodeUnderPressure(node))
}

func handleNodeUpdate(old, current interface{}) {
	// Cache access example
	nodeInterface, exists, err := store.GetByKey("minikube")
	if exists && err == nil {
		logrus.Debugf("Found the node [%v] in cache", nodeInterface)
	}

	node := current.(*api.Node)
	logrus.Infof("Node [%s] memory pressure state is [%v]", node.Name, isNodeUnderPressure(node))
}

func pollNodes() error {
	for {
		nodes, err := clientset.CoreV1().Nodes().List(v1.ListOptions{FieldSelector: "metadata.name=minikube"})
		if err != nil {
			logrus.Warnf("Failed to poll the nodes: %v", err)
			continue
		}
		if len(nodes.Items) > 0 {
			node := nodes.Items[0]
			node.Annotations["checked"] = "true"
			_, err := clientset.CoreV1().Nodes().Update(&node)
			if err != nil {
				logrus.Warnf("Failed to update the node: %v", err)
				continue
			}
		}
		for _, node := range nodes.Items {
			//checkImageStorage(&node)
			logrus.Infof("Node [%s] memory pressure state is [%v]", node.Name, isNodeUnderPressure(&node))

		}
		time.Sleep(10 * time.Second)
	}
}

func isNodeUnderPressure(node *api.Node) bool {
	memoryPressure := false
	for _, condition := range node.Status.Conditions {
		if condition.Type == "MemoryPressure" {
			if condition.Status == "True" {
				memoryPressure = true
			}
			break
		}
	}
	return memoryPressure
}

func podCreated(obj interface{}) {
	pod := obj.(*api.Pod)
	logrus.Infof("Pod with [%s] name created on node [%s]: ", pod.ObjectMeta.Name, pod.Spec.NodeName)
}
func podDeleted(obj interface{}) {
	pod := obj.(*api.Pod)
	logrus.Infof("Pod with [%s] name deleted from node [%s]: ", pod.ObjectMeta.Name, pod.Spec.NodeName)
}

func getClient(pathToCfg string) (*kubernetes.Clientset, error) {
	var config *rest.Config
	var err error
	if pathToCfg == "" {
		logrus.Info("Using in cluster config")
		config, err = rest.InClusterConfig()
		// in cluster access
	} else {
		logrus.Info("Using out of cluster config")
		config, err = clientcmd.BuildConfigFromFlags("", pathToCfg)
	}
	if err != nil {
		return nil, err
	}
	//return kubernetes.NewForConfig(config)
	return kubernetes.NewForConfigOrDie(config), nil
}
