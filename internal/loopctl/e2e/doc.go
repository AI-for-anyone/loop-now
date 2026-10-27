// Package e2e 是 loopctl 与真实服务端的端到端测试（tasks 第 9、10 节），只有测试代码。
// 测试在进程内启动服务端，使用测试库、测试 Redis 与 fakepi，不连接本机正在运行的开发服务端。
package e2e
