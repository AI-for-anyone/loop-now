# 上游响应反射防护增量

2026-10-20，仅修改独占`internal/server/larksidecar/`并做定向组件验证。问题来自静态审查和合成provider反射，不是官方现场泄漏报告。

## 修复边界

- 上游非2xx不再透传原body/header。400–599仅保数字HTTP状态，body固定`lark_sidecar_rejected`；3xx继续502且不跟随，其他异常状态归502。不会解析或记录原provider message。
- 2xx保留有限QA JSON业务响应，所有当前请求实际使用的已提交access token完整原文字节匹配均被替换，包括跨Read边界、重复和相邻匹配。不是泛token正则；其他消息、其他形似token的文字和分页字段不变。
- 滚动缓冲跨Read只保留至多token长度减一的后缀；每Read最大32KiB，总上游字节及最终下游字节分别受原ResponseBytes上限约束，仍有原total/idle超时。真EOF才输出尾部；超限、截断、取消丢弃待续尾部并中止响应，不伪造完整成功。
- 替换为不在该token中出现的JSON安全字符重复四次，通常`****`。这种字符屏障不会像固定标记那样在前后普通字节之间重新拼出原token。极端无安全字符可选则在发2xx头之前固定502。此机制不声称识别编码/变换过的秘密或其他未交给broker的秘密。
- 响应Content-Type固定application/json，不回显可能反射token的原Content-Type参数或其他上游头；上游请求已要求identity且transport禁自动解压，响应如仍声明非identity编码则固定502，防压缩内容绕过原文字节过滤。本stage仅覆盖现有JSON QA文本/分页API，不新增媒体/任意二进制流能力。

## 定向证据

- `TestBrokerReflectedAccessTokenNeverReturned`：真实自持TLS provider从合成Authorization取值并故意反射到错误JSON、分块流和Content-Type参数；HTTP401/403/500仅固定错误，HTTP200错误形JSON只替换当前token，保留code和分页字段。四格Source各一次；响应body/headers均无完整合成token。无真实token/供应商。
- `TestExactTokenRedactionEveryBoundary`：强制每种分片宽度（含逐字节）经过真实过滤组件，覆盖跨边界、重复/相邻匹配、普通形似token和不完整前缀不变；检查每Read后待续长度小于token长度。
- `TestExactTokenMaskCannotRecreateSecret`：合成token本身含常见固定redaction标记，证明动态字符屏障不会在替换边界重造secret。
- `TestBrokerNormalPaginationPreservedAndEncodedResponseRejected`：正常消息和has_more/page_token原字节保持；自持gzip反射被固定502拒绝。
- 同一命令复验原`TestResponseBoundsAndCancellation`及`TestBrokerFreeze*`/`TestBrokerBusy*`，确认超限/取消与active计数/Freeze/Close+Wait收尾不回归。

命令选择器：`'^(TestExactToken|TestBrokerReflected|TestBrokerNormal|TestResponseBoundsAndCancellation|TestBrokerFreeze|TestBrokerBusy)'`，普通PASS **0.812s**，race PASS **1.927s**，零skip。日志为`.local-build/lark-user-variant-proof/response-secrets-ordinary-final2.log`与`response-secrets-race-final.log`；此前本轮迭代日志保留。`go vet ./internal/server/larksidecar ./scripts/lark-user-variant/launcher`通过。

没有重跑原native矩阵，没有真实Pi/App/登录/账号/现库/服务操作或Git。独立launcher/exec-manager及双listener准入尚有既有待完成项，本响应组件检查不能替代它们；旧010工件manifest不覆盖本增量，未发supported安装包。测试自持HTTP/TLS监听、客户端、handler与临时目录按各fixture清理。
