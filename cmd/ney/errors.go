package main

import (
	"fmt"
	"os"
	"strings"
)

// printCLIError renders an error with an actionable hint instead of cobra's
// raw error + usage dump.
func printCLIError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s %v\n", Red("✗"), err)
	for _, hint := range friendlyHints(err) {
		fmt.Fprintf(os.Stderr, "  %s %s\n", Dim("→"), hint)
	}
}

// friendlyHints maps common failure text to next steps a user can act on.
func friendlyHints(err error) []string {
	msg := err.Error()
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(msg, s) {
				return true
			}
		}
		return false
	}

	switch {
	case has("connection refused", "i/o timeout", "no such host", "network is unreachable"):
		return []string{
			"Can't reach your model server — is it running (Ollama / LM Studio)?",
			"Check the endpoint in ~/.ney/config.yaml (ney config edit), or run: ney init",
			"Diagnose with: ney doctor",
		}
	case has("OPENAI_API_KEY not set", "GEMINI_API_KEY not set", "COHERE_API_KEY not set", "JINA_API_KEY not set"):
		return []string{
			"Set the key shown above as an environment variable",
			"Or switch to a local provider (ollama / lmstudio): ney init",
		}
	case has("embedder mismatch", "backend mismatch"):
		return []string{"Rebuild the index: ney reset && ney index <path>"}
	case has("unknown embedder provider"):
		return []string{"Fix ~/.ney/config.yaml (ney config edit) or rerun setup: ney init"}
	case has("No models loaded"):
		return []string{
			"Your server has no model loaded — load one in LM Studio (Developer tab) or run: lms load <model>",
			"Then check with: ney doctor",
		}
	case has("is writing — stop it first"):
		return []string{
			"Wait for the other process to finish, or stop it and rerun this command",
			"Check status with: ney status",
		}
	case has("status 401", "status 403"):
		return []string{"The provider rejected your API key — check the relevant *_API_KEY env var"}
	case has("status 404"):
		return []string{"The model was not found on the server — check the model name (ney models) or load it in your server"}
	}
	return nil
}
