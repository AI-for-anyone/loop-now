package taskmodel

// PublicError uses fixed descriptions: provider stderr and credential-file errors
// are untrusted and may contain keys, environment values or private paths.
// Keeping the code and message/run IDs preserves failure attribution without
// depending on credentials still being decryptable after instance retirement.
func PublicError(code, message *string) *string {
	if message == nil {
		return nil
	}
	safe := "任务回合执行失败"
	if code != nil {
		switch *code {
		case "session_missing":
			safe = "任务会话文件缺失，未创建替代会话"
		case "pi_error":
			safe = "Pi 返回执行错误"
		case "pi_exited":
			safe = "Pi 进程在回复期间退出"
		case "pi_start_failed":
			safe = "Pi 进程启动失败"
		case "credential_unavailable":
			safe = "任务冻结凭证不可用"
		case "cli_install_failed":
			safe = "任务 CLI 安装失败"
		case "workspace_sync_failed":
			safe = "任务工作目录同步失败"
		case "server_restarted":
			safe = "服务重启，未重放该轮"
		case "internal_error":
			safe = "执行基础设施失败"
		case "user_stopped":
			safe = "已被人停止"
		}
	}
	return &safe
}
