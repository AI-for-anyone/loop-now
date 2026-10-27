// 读取固定版本 Pi 依赖的 pi-ai 包中内置的模型目录，输出 JSON 到标准输出。
// 用法：node catalog.mjs <pi-ai 目录>
// 只读取本地文件，不发起网络请求，也不读写 Pi 的配置目录。
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const dir = process.argv[2];
if (!dir) {
	console.error("缺少 pi-ai 目录参数");
	process.exit(2);
}

const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
const all = await import(pathToFileURL(join(dir, "dist", "providers", "all.js")).href);
const envKeys = await import(pathToFileURL(join(dir, "dist", "env-api-keys.js")).href);
const thinking = await import(pathToFileURL(join(dir, "dist", "models.js")).href);

// 传入“任意变量都存在”的环境，让 findEnvKeys 返回该提供方的全部 API key 变量名。
const anyEnv = new Proxy({}, { get: () => "x", has: () => true });

const authProviders=new Map(all.builtinProviders().map(p=>[p.id,p]));
const providers = [];
for (const id of all.getBuiltinProviders()) {
	const models = all.getBuiltinModels(id).map((m) => ({
		id: String(m.id),
		name: String(m.name ?? m.id),
		reasoning: Boolean(m.reasoning),
		thinkingLevels: thinking.getSupportedThinkingLevels(m),
		defaultThinkingLevel: thinking.clampThinkingLevel(m, "medium"),
		contextWindow: Number(m.contextWindow ?? 0),
		maxTokens: Number(m.maxTokens ?? 0),
		input: Array.isArray(m.input) ? m.input.map(String) : [],
	}));
	const authMethods=[];
 if (authProviders.get(id)?.auth?.apiKey?.login) authMethods.push("api_key");
 if (id==="openai-codex" && authProviders.get(id)?.auth?.oauth) authMethods.push("oauth");
 providers.push({ id: String(id), authMethods, apiKeyEnvVars: envKeys.findEnvKeys(id, anyEnv) ?? [], models });
}

const generatedAt = all.getBuiltinModelDataGeneratedAt();
process.stdout.write(
	JSON.stringify({
		piAIVersion: pkg.version,
		generatedAt: typeof generatedAt === "number" ? new Date(generatedAt).toISOString() : null,
		providers,
	}),
);
