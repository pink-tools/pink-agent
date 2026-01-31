package cli

import (
	"fmt"

	"github.com/pink-tools/pink-core"
)

func HandleTokens(args []string) error {
	response, err := core.SendCommand(serviceName, "getContextTokens")
	if err != nil {
		return fmt.Errorf("agent not running")
	}

	fmt.Println(response)
	return nil
}
