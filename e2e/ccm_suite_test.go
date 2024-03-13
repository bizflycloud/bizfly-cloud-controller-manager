package e2e_test

import (
	"context"
	"e2e_test/test/framework"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bizflycloud/gobizfly"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	useExisting   = false
	reuse         = false
	clusterName   string
	authMethod    string
	appCredId     string
	appCredSecret string
	basicAuth     string
	region        = "HN"
	apiUrl        = "https://manage.bizflycloud.vn"
	ctx           context.Context
)

func init() {
	flag.StringVar(&framework.Image, "image", framework.Image, "registry/repository:tag")
	flag.BoolVar(&reuse, "reuse", reuse, "Create a cluster and continue to use it")
	flag.BoolVar(&useExisting, "use-existing", useExisting, "Use an existing kubernetes cluster")
	flag.StringVar(&framework.KubeConfigFile, "kubeconfig", os.Getenv("KUBECONFIG"), "To use existing cluster provide kubeconfig file")
	flag.StringVar(&region, "region", region, "Region to create load balancers")
	flag.DurationVar(&framework.Timeout, "timeout", 5*time.Minute, "Timeout for a test to complete successfully")
	flag.StringVar(&apiUrl, "bizfly-url", apiUrl, "The Bizfly API URL to send requests to")
	flag.StringVar(&basicAuth, "basic-auth", basicAuth, "Bizflycloud's basic auth")
	flag.StringVar(&authMethod, "auth-method", authMethod, "Bizflycloud's auth method")
	flag.StringVar(&appCredId, "app-cred-id", appCredId, "App Credential Id")
	flag.StringVar(&appCredSecret, "app-cred-secret", appCredSecret, "App Credential Secret")
	flag.StringVar(&clusterName, "cluster-uid", clusterName, "Cluster's uid")
}

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(framework.Timeout)
	RunSpecs(t, "E2e Suite")
}

var root *framework.Framework

func GetApiClient() (*gobizfly.Client, error) {
	client, err := gobizfly.NewClient(gobizfly.WithAPIUrl(apiUrl), gobizfly.WithRegionName(region), gobizfly.WithBasicAuth(basicAuth))
	if err != nil {
		return nil, fmt.Errorf("Cannot create BizFly Cloud Client: %w", err)
	}

	ctx, cancelFunc := context.WithTimeout(context.Background(), 0)
	defer cancelFunc()
	token, err := client.Token.Init(
		ctx,
		&gobizfly.TokenCreateRequest{
			AuthMethod:    authMethod,
			AppCredID:     appCredId,
			AppCredSecret: appCredSecret,
		})
	if err != nil {
		return nil, fmt.Errorf("Cannot create token: %w", err)
	}

	client.SetKeystoneToken(token)

	return client, nil
}

var _ = BeforeSuite(func() {
	By("Using kubeconfig from " + framework.KubeConfigFile)
	fmt.Println(framework.KubeConfigFile)
	config, err := clientcmd.BuildConfigFromFlags("", framework.KubeConfigFile)
	Expect(err).NotTo(HaveOccurred())

	// Client
	kubeClient := kubernetes.NewForConfigOrDie(config)
	By("url " + apiUrl)
	bizflyClient, _ := GetApiClient()

	// Framework
	root = framework.New(config, kubeClient, *bizflyClient)

	fmt.Println(root.Namespace())
	err = root.CreateNamespace()
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	if !(useExisting || reuse) {
		By("Deleting cluster")
		err := framework.DeleteCluster(clusterName)
		Expect(err).NotTo(HaveOccurred())
	} else {
		By("Deleting Namespace " + root.Namespace())
		err := root.DeleteNamespace()
		Expect(err).NotTo(HaveOccurred())

		By("Not deleting cluster")
	}
})
