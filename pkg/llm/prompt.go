package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BuildPrompt combines the base prompt with context data.
// This function is shared across all LLM providers to ensure consistent prompt formatting.
func BuildPrompt(request *AnalysisRequest) (string, error) {
	var promptBuilder strings.Builder

	promptBuilder.WriteString("Keep your response concise and focused. " +
		"Your entire response must not exceed 65,000 characters — it will be " +
		"displayed inside a GitHub check-run which enforces that limit.\n\n")
	promptBuilder.WriteString("When this analysis runs in a checked-out repository and you identify a " +
		"clean, concrete fix, apply the fix by editing the repository files. Do not only describe " +
		"or suggest the change. Do not commit or push changes; the analysis runner will capture " +
		"the resulting git diff for the Fix it button. If no safe fix is clear, leave the working " +
		"tree unchanged and explain why.\n\n")
	promptBuilder.WriteString(request.Prompt)
	promptBuilder.WriteString("\n\n")

	if len(request.Context) > 0 {
		promptBuilder.WriteString("Context Information:\n")

		for key, value := range request.Context {
			fmt.Fprintf(&promptBuilder, "=== %s ===\n", strings.ToUpper(key))

			switch v := value.(type) {
			case string:
				promptBuilder.WriteString(v)
			case map[string]any, []any:
				jsonData, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					return "", fmt.Errorf("failed to marshal context %s: %w", key, err)
				}
				promptBuilder.Write(jsonData)
			default:
				fmt.Fprintf(&promptBuilder, "%v", v)
			}

			promptBuilder.WriteString("\n\n")
		}
	}

	return promptBuilder.String(), nil
}
