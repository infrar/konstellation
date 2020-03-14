package kubedash

import (
	"fmt"

	"github.com/davidzhao/konstellation/pkg/utils/cli"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	LOCAL_URL = "http://localhost:8001/api/v1/namespaces/kubernetes-dashboard/services/https:kubernetes-dashboard:/proxy/"
)

type KubeDash struct {
}

func (d *KubeDash) Name() string {
	return "kube.dashboard"
}

func (d *KubeDash) Version() string {
	// TODO: should we match Kube versions
	return "v2.0.0-beta8"
}

func (d *KubeDash) NeedsCLI() bool {
	return false
}

func (d *KubeDash) InstallCLI() error {
	return nil
}

func (d *KubeDash) InstallComponent(kclient client.Client) error {
	url := fmt.Sprintf("https://raw.githubusercontent.com/kubernetes/dashboard/%s/aio/deploy/recommended.yaml",
		d.Version())
	return cli.KubeApply(url)
}
