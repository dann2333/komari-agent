//go:build !unix

package cmd

import (
	"io"
	"log"
)

// silenceOutput 在没有 dup2 的平台上先只关掉标准 logger。
func silenceOutput() {
	log.SetOutput(io.Discard)
}
