package main

import (
	"context"
	"errors"
	"strings"
)

func userFacingDesktopError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "요청 시간이 초과되었습니다. 잠시 후 다시 시도하세요."
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "proxy"), strings.Contains(message, "soxy"), strings.Contains(message, "socks"):
		return "프록시 설정이나 연결 상태를 확인하세요."
	case strings.Contains(message, "hook"), strings.Contains(message, "webhook"), strings.Contains(message, "discord"), strings.Contains(message, "slack"):
		return "외부 알림 설정을 확인하세요."
	case strings.Contains(message, "credential"), strings.Contains(message, "authenticate"), strings.Contains(message, "login"):
		return "CGV 로그인에 실패했습니다. 로그인 정보를 확인하고 다시 시도하세요."
	case strings.Contains(message, "connect"), strings.Contains(message, "dial"), strings.Contains(message, "network"):
		return "Cineko 서비스에 연결할 수 없습니다. 잠시 후 다시 시도하세요."
	default:
		return "요청을 처리하지 못했습니다. 잠시 후 다시 시도하세요."
	}
}
