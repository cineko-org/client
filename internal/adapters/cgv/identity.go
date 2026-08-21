package cgv

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const providerCGV = "cgv"

func catalogID(providerID, kind, sourceKey string) string {
	normalized := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(providerID), strings.TrimSpace(kind), strings.TrimSpace(sourceKey),
	}, "\x00"))
	digest := sha256.Sum256([]byte(normalized))
	return strings.TrimSpace(kind) + "_" + hex.EncodeToString(digest[:16])
}

func seatID(auditoriumID, label string) string {
	return catalogID("catalog", "seat", strings.TrimSpace(auditoriumID)+"\x00"+strings.ToUpper(strings.TrimSpace(label)))
}
