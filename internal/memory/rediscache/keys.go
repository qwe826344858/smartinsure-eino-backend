package rediscache

import "fmt"

func MessageIDsKey(sessionID string) string {
	return fmt.Sprintf("chat:session:%s:message_ids", sessionID)
}

func MessagesKey(sessionID string) string {
	return fmt.Sprintf("chat:session:%s:messages", sessionID)
}

func SessionStateKey(sessionID string) string {
	return fmt.Sprintf("chat:session:%s:state", sessionID)
}

func SessionOwnerKey(sessionID string) string {
	return fmt.Sprintf("chat:session:%s:owner", sessionID)
}

func LatestAnonymousSessionKey(anonymousID string) string {
	return fmt.Sprintf("chat:anon:%s:latest_session", anonymousID)
}
