package ansiblerunner

import (
	"bytes"
	"context"
	"log"
	"time"

	"github.com/apenella/go-ansible/v2/pkg/execute"
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

func (ar *AnsibleRunner) RunPlaybook(playbookPath string) (string, string, error) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Minute,
	)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	opts := &playbook.AnsiblePlaybookOptions{
		Inventory:  ar.VPS.Ip + ",",
		User:       ar.VPS.User,
		PrivateKey: ar.PrivateKeyPath,
		Become:     true,
	}

	cmd := playbook.NewAnsiblePlaybookCmd(
		playbook.WithPlaybooks(playbookPath),
		playbook.WithPlaybookOptions(opts),
	)

	executor := execute.NewDefaultExecute(
		execute.WithCmd(cmd),
		execute.WithWrite(&stdout),
		execute.WithWriteError(&stderr),
	)

	err := executor.Execute(ctx)
	if err != nil {
		log.Printf("Ansible execution failed: %v", err)
	}

	return stdout.String(), stderr.String(), err
}
