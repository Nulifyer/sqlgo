package db

import "strings"

func replaceAll(value, old, new string) string {
	return strings.ReplaceAll(value, old, new)
}
