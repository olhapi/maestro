package kanban

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func generateID(prefix string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
