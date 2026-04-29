package api

import "os"

var scriptCache = make(map[string]string)

func ReadScript(path string) (string, error) {
	if c, ok := scriptCache[path]; ok { return c, nil }
	data, err := os.ReadFile(path)
	if err != nil { return "", err }
	scriptCache[path] = string(data)
	return scriptCache[path], nil
}

func ReadClientScript(path string) (string, error) {
	return ReadScript(path)
}
