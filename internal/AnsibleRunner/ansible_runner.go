package ansiblerunner

import (
	"context"
	"log"
	"time"

	"github.com/apenella/go-ansible/v2/pkg/playbook"

	models "github.com/20vikash/NotYourRAM/internal/models"
)

type AnsibleRunner struct {
	VPS            *models.VPS
	PrivateKeyPath string
}

func NewAnsibleRunner(vps *models.VPS, privateKeyPath string) *AnsibleRunner {
	return &AnsibleRunner{
		VPS:            vps,
		PrivateKeyPath: privateKeyPath,
	}
}

func (ar *AnsibleRunner) RunPlaybook(playbookPath string) error {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Minute,
	)
	defer cancel()

	opts := &playbook.AnsiblePlaybookOptions{
		Inventory:  ar.VPS.Ip + ",",
		User:       ar.VPS.User,
		PrivateKey: ar.PrivateKeyPath,
		Become:     true,
	}

	err := playbook.NewAnsiblePlaybookExecute(playbookPath).
		WithPlaybookOptions(opts).
		Execute(ctx)

	if err != nil {
		log.Println(err)
	}

	return nil
}
