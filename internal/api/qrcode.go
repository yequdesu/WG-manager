package api

import (
	"bytes"
	"os/exec"
	"strings"
)

// generateQR returns an SVG QR code for the given data using qrencode.
// If qrencode is not available, returns a plain-text fallback.
func generateQR(data string) string {
	cmd := exec.Command("qrencode", "-s", "8", "-o", "-", "-t", "SVG", "--", data)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return qrFallback(data)
	}
	return strings.TrimSpace(out.String())
}

func qrFallback(data string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="200" viewBox="0 0 400 200">
<rect width="100%" height="100%" fill="white"/>
<text x="200" y="80" text-anchor="middle" font-family="monospace" font-size="12" fill="red">qrencode not installed.</text>
<text x="200" y="110" text-anchor="middle" font-family="monospace" font-size="11" fill="black">Run: apt install qrencode</text>
<text x="200" y="140" text-anchor="middle" font-family="monospace" font-size="10" fill="gray">WireGuard config:</text>
<text x="200" y="165" text-anchor="middle" font-family="monospace" font-size="9" fill="gray">` + data[:min(len(data), 80)] + `...</text>
</svg>`
}
