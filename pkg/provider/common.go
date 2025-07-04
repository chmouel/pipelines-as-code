package provider

import (
	"bytes"
	"log"
	"text/template"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const (
	DefaultStatusTemplate = `<b>PipelineRun: {{ .PipelineRun.ObjectMeta.Name }}</b>\n<table>\n  <tr><th>Status</th><th>Duration</th><th>Name</th></tr>\n{{- range $taskrun := .TaskRunList }}\n<tr>\n<td>{{ formatCondition $taskrun.PipelineRunTaskRunStatus.Status.Conditions }}</td>\n<td>{{ formatDuration $taskrun.PipelineRunTaskRunStatus.Status.StartTime $taskrun.PipelineRunTaskRunStatus.Status.CompletionTime }}</td><td>\n{{ $taskrun.ConsoleLogURL }}\n</td></tr>\n{{- end }}\n</table>`
	StatusSummaryMaxLen   = 65535
)

// RenderStatusSummary renders a status summary using a Go template, helpers, and the PipelineRun context.
// If the template is invalid or execution fails, it falls back to the default template.
// Output is truncated to StatusSummaryMaxLen with a warning if needed.
func RenderStatusSummary(pr *tektonv1.PipelineRun, templateStr string) string {
	tmplStr := DefaultStatusTemplate
	if templateStr != "" {
		tmplStr = templateStr
	}
	funcMap := template.FuncMap{
		"formatDuration":  formatting.Duration,
		"formatCondition": formatting.ConditionEmoji,
	}
	data := map[string]interface{}{
		"PipelineRun": pr,
	}
	var buf bytes.Buffer
	tmpl, err := template.New("summary").Funcs(funcMap).Parse(tmplStr)
	if err != nil {
		log.Printf("[PAC] Custom status summary template parse error: %v. Falling back to default template.", err)
		tmpl, _ = template.New("summary").Funcs(funcMap).Parse(DefaultStatusTemplate)
		_ = tmpl.Execute(&buf, data)
		if buf.Len() == 0 && pr != nil {
			return "PipelineRun: " + pr.ObjectMeta.Name
		}
		if buf.Len() == 0 {
			log.Printf("[PAC] Default status summary template also failed to render. Returning minimal fallback.")
			return "Failed to render summary: template execution error"
		}
		return buf.String()
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("[PAC] Status summary template execution error: %v. Falling back to default template.", err)
		buf.Reset()
		tmpl, _ = template.New("summary").Funcs(funcMap).Parse(DefaultStatusTemplate)
		if err2 := tmpl.Execute(&buf, data); err2 != nil {
			log.Printf("[PAC] Default status summary template also failed to render: %v. Returning minimal fallback.", err2)
			if pr != nil {
				return "PipelineRun: " + pr.ObjectMeta.Name
			}
			return "Failed to render summary: template execution error"
		}
		if buf.Len() == 0 && pr != nil {
			return "PipelineRun: " + pr.ObjectMeta.Name
		}
		if buf.Len() == 0 {
			log.Printf("[PAC] Default status summary template rendered empty output. Returning minimal fallback.")
			return "Failed to render summary: template execution error"
		}
		return buf.String()
	}
	out := buf.String()
	if len(out) > StatusSummaryMaxLen {
		truncMsg := "\n\n**WARNING: Output truncated to fit provider's 65,535 character limit.**"
		out = out[:StatusSummaryMaxLen-len(truncMsg)] + truncMsg
	}
	if out == "" {
		if pr != nil {
			return "PipelineRun: " + pr.ObjectMeta.Name
		}
		return "No PipelineRun information available."
	}
	return out
}
