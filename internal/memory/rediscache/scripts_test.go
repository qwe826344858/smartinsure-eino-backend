package rediscache

import (
	"strings"
	"testing"
)

func TestAppendMessageLuaContainsAtomicOperations(t *testing.T) {
	for _, token := range []string{"HSET", "RPUSH", "LLEN", "LPOP", "HDEL", "EXPIRE"} {
		if !strings.Contains(appendMessageLua, token) {
			t.Fatalf("appendMessageLua missing %s", token)
		}
	}
}

func TestDeleteLastIfExpectedLuaContainsExpectedOperations(t *testing.T) {
	for _, token := range []string{"LINDEX", "RPOP", "HDEL", "EXPIRE", "expectedID"} {
		if !strings.Contains(deleteLastIfExpectedLua, token) {
			t.Fatalf("deleteLastIfExpectedLua missing %s", token)
		}
	}
}
