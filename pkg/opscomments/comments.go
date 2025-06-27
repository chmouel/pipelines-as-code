package opscomments

import (
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
)

var (
	testAllRegex      = regexp.MustCompile(`(?m)^/test\s*$`)
	retestAllRegex    = regexp.MustCompile(`(?m)^/retest\s*$`)
	testSingleRegex   = regexp.MustCompile(`(?m)^/test[ \t]+\S+`)
	retestSingleRegex = regexp.MustCompile(`(?m)^/retest[ \t]+\S+`)
	oktotestRegex     = regexp.MustCompile(`(?m)^/ok-to-test\s*$`)
	cancelAllRegex    = regexp.MustCompile(`(?m)^(/cancel)\s*$`)
	cancelSingleRegex = regexp.MustCompile(`(?m)^(/cancel)[ \t]+\S+`)
	llmRegex          = regexp.MustCompile(`(?m)^/llm\s+(.+)$`)
)

type EventType string

func (e EventType) String() string {
	return string(e)
}

var (
	NoOpsCommentEventType        = EventType("no-ops-comment")
	TestAllCommentEventType      = EventType("test-all-comment")
	TestSingleCommentEventType   = EventType("test-comment")
	RetestSingleCommentEventType = EventType("retest-comment")
	RetestAllCommentEventType    = EventType("retest-all-comment")
	OnCommentEventType           = EventType("on-comment")
	CancelCommentSingleEventType = EventType("cancel-comment")
	CancelCommentAllEventType    = EventType("cancel-all-comment")
	OkToTestCommentEventType     = EventType("ok-to-test-comment")
	LLMCommentEventType          = EventType("llm-comment")
	LLMQueryEventType            = EventType("llm-query")
	LLMPRAnalysisEventType       = EventType("llm-pr-analysis")
)

const (
	testComment   = "/test"
	retestComment = "/retest"
	cancelComment = "/cancel"
)

// CommentEventType returns the event type based on the comment content.
func CommentEventType(comment string) EventType {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return NoOpsCommentEventType
	}

	// Check for LLM commands first
	if llmRegex.MatchString(comment) {
		llmCommand := GetLLMCommand(comment)
		if isPRAnalysisCommand(llmCommand) {
			return LLMPRAnalysisEventType
		}
		return LLMCommentEventType
	}

	// Check for test commands
	if testAllRegex.MatchString(comment) {
		return TestAllCommentEventType
	}
	if testSingleRegex.MatchString(comment) {
		return TestSingleCommentEventType
	}

	// Check for retest commands
	if retestAllRegex.MatchString(comment) {
		return RetestAllCommentEventType
	}
	if retestSingleRegex.MatchString(comment) {
		return RetestSingleCommentEventType
	}

	// Check for cancel commands
	if cancelAllRegex.MatchString(comment) {
		return CancelCommentAllEventType
	}
	if cancelSingleRegex.MatchString(comment) {
		return CancelCommentSingleEventType
	}

	// Check for ok-to-test command
	if oktotestRegex.MatchString(comment) {
		return OkToTestCommentEventType
	}

	return NoOpsCommentEventType
}

// isPRAnalysisCommand checks if the LLM command is requesting PR analysis.
func isPRAnalysisCommand(command string) bool {
	command = strings.ToLower(command)
	prAnalysisKeywords := []string{
		"analyze this pull request",
		"analyze this pr",
		"review this pull request",
		"review this pr",
		"check this pull request",
		"check this pr",
		"security issues",
		"security vulnerabilities",
		"security bugs",
		"security concerns",
		"any issues",
		"any problems",
		"any bugs",
		"code review",
		"code analysis",
	}

	for _, keyword := range prAnalysisKeywords {
		if strings.Contains(command, keyword) {
			return true
		}
	}
	return false
}

// SetEventTypeAndTargetPR function will set the event type and target test pipeline run in an event.
func SetEventTypeAndTargetPR(event *info.Event, comment string) {
	commentType := CommentEventType(comment)
	if commentType == RetestSingleCommentEventType || commentType == TestSingleCommentEventType {
		event.TargetTestPipelineRun = GetPipelineRunFromTestComment(comment)
	}
	if commentType == CancelCommentAllEventType || commentType == CancelCommentSingleEventType {
		event.CancelPipelineRuns = true
	}
	if commentType == CancelCommentSingleEventType {
		event.TargetCancelPipelineRun = GetPipelineRunFromCancelComment(comment)
	}
	event.EventType = commentType.String()
	event.TriggerComment = comment
}

func IsOkToTestComment(comment string) bool {
	return oktotestRegex.MatchString(comment)
}

// EventTypeBackwardCompat handle the backward compatibility we need to keep until
// we have done the deprecated notice
//
// 2024-07-01 chmouel
//
//	set anyOpsComments to pull_request see https://issues.redhat.com/browse/SRVKP-5775
//	we keep on-comment to the "on-comment" type
func EventTypeBackwardCompat(eventEmitter *events.EventEmitter, repo *v1alpha1.Repository, label string) string {
	if label == OnCommentEventType.String() {
		return label
	}
	if IsAnyOpsEventType(label) {
		eventEmitter.EmitMessage(repo, zap.WarnLevel, "DeprecatedOpsComment",
			fmt.Sprintf("the %s event type is deprecated, this will be changed to %s in the future",
				label, triggertype.PullRequest.String()))
		return triggertype.PullRequest.String()
	}
	return label
}

func IsAnyOpsEventType(eventType string) bool {
	return eventType == TestSingleCommentEventType.String() ||
		eventType == TestAllCommentEventType.String() ||
		eventType == RetestAllCommentEventType.String() ||
		eventType == RetestSingleCommentEventType.String() ||
		eventType == CancelCommentSingleEventType.String() ||
		eventType == CancelCommentAllEventType.String() ||
		eventType == OkToTestCommentEventType.String() ||
		eventType == OnCommentEventType.String() ||
		eventType == LLMCommentEventType.String() ||
		eventType == LLMQueryEventType.String()
}

// AnyOpsKubeLabelInSelector will output a Kubernetes label out of all possible
// CommentEvent Type for selection.
func AnyOpsKubeLabelInSelector() string {
	return fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s,%s,%s",
		TestSingleCommentEventType.String(),
		TestAllCommentEventType.String(),
		RetestAllCommentEventType.String(),
		RetestSingleCommentEventType.String(),
		CancelCommentSingleEventType.String(),
		CancelCommentAllEventType.String(),
		OkToTestCommentEventType.String(),
		OnCommentEventType.String(),
		LLMCommentEventType.String(),
		LLMQueryEventType.String())
}

func GetPipelineRunFromTestComment(comment string) string {
	if strings.Contains(comment, testComment) {
		return getNameFromComment(testComment, comment)
	}
	return getNameFromComment(retestComment, comment)
}

func GetPipelineRunFromCancelComment(comment string) string {
	return getNameFromComment(cancelComment, comment)
}

// GetLLMCommand extracts the command part from an LLM comment
// e.g., "/llm restart the go test pipeline" returns "restart the go test pipeline".
func GetLLMCommand(comment string) string {
	matches := llmRegex.FindStringSubmatch(comment)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func getNameFromComment(typeOfComment, comment string) string {
	splitTest := strings.Split(strings.TrimSpace(comment), typeOfComment)
	if len(splitTest) < 2 {
		return ""
	}
	// now get the first line
	getFirstLine := strings.Split(splitTest[1], "\n")

	// and the first argument
	firstArg := strings.Split(getFirstLine[0], " ")
	if len(firstArg) < 2 {
		return ""
	}

	// trim spaces
	return strings.TrimSpace(firstArg[1])
}

func GetPipelineRunAndBranchNameFromTestComment(comment string) (string, string, error) {
	if strings.Contains(comment, testComment) {
		return getPipelineRunAndBranchNameFromComment(testComment, comment)
	}
	return getPipelineRunAndBranchNameFromComment(retestComment, comment)
}

func GetPipelineRunAndBranchNameFromCancelComment(comment string) (string, string, error) {
	return getPipelineRunAndBranchNameFromComment(cancelComment, comment)
}

// getPipelineRunAndBranchNameFromComment function will take GitOps comment and split the comment
// by /test, /retest or /cancel to return branch name and pipelinerun name.
func getPipelineRunAndBranchNameFromComment(typeOfComment, comment string) (string, string, error) {
	var prName, branchName string
	splitTest := strings.Split(comment, typeOfComment)

	// after the split get the second part of the typeOfComment (/test, /retest or /cancel)
	// as second part can be branch name or pipelinerun name and branch name
	// ex: /test branch:nightly, /test prname branch:nightly
	if splitTest[1] != "" && strings.Contains(splitTest[1], ":") {
		branchData := strings.Split(splitTest[1], ":")

		// make sure no other word is supported other than branch word
		if !strings.Contains(branchData[0], "branch") {
			return prName, branchName, fmt.Errorf("the GitOps comment%s does not contain a branch word", branchData[0])
		}
		branchName = strings.Split(strings.TrimSpace(branchData[1]), " ")[0]

		// if data after the split contains prname then fetch that
		prData := strings.Split(strings.TrimSpace(branchData[0]), " ")
		if len(prData) > 1 {
			prName = strings.TrimSpace(prData[0])
		}
	} else {
		// get the second part of the typeOfComment (/test, /retest or /cancel)
		// as second part contains pipelinerun name
		// ex: /test prname
		getFirstLine := strings.Split(splitTest[1], "\n")
		// trim spaces
		prName = strings.TrimSpace(getFirstLine[0])
	}
	return prName, branchName, nil
}
