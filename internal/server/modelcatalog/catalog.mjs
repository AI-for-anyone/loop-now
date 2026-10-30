// 用固定版本 Pi 的原生 ModelRuntime 合并内置目录与 models.json。
// 用法：node catalog.mjs <pi-ai 目录> [models.json 路径 <Pi 主程序目录>]
// 只读取本地文件，不发起网络请求，也不读取真实用户配置。
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const dir = process.argv[2];
const modelsPath = process.argv[3];
const agentDir = process.argv[4];
if (!dir || (modelsPath && !agentDir)) {
	console.error("缺少 pi-ai 目录或 Pi 主程序目录参数");
	process.exit(2);
}

const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
const all = await import(pathToFileURL(join(dir, "dist", "providers", "all.js")).href);
const envKeys = await import(pathToFileURL(join(dir, "dist", "env-api-keys.js")).href);
const thinking = await import(pathToFileURL(join(dir, "dist", "models.js")).href);
let runtime;
let configValues;
if (modelsPath) {
	const { ModelRuntime } = await import(pathToFileURL(join(agentDir, "dist", "core", "model-runtime.js")).href);
	configValues = await import(pathToFileURL(join(agentDir, "dist", "core", "resolve-config-value.js")).href);
	runtime = await ModelRuntime.create({
		authPath: join(modelsPath, "..", "unused-auth.json"),
		modelsPath,
		refreshOnCreate: false,
		allowModelNetwork: false,
	});
	if (runtime.getError()) throw new Error(runtime.getError());
}

// 传入“任意变量都存在”的环境，让 findEnvKeys 返回内置提供方的全部 API key 变量名。
const anyEnv = new Proxy({}, { get: () => "x", has: () => true });
const configured = modelsPath ? JSON.parse(readFileSync(modelsPath, "utf8")) : { providers: {} };
const effective = runtime ? runtime.getProviders() : all.builtinProviders();
const providers = [];
for (const provider of effective) {
	const id = String(provider.id);
	const models = (runtime ? runtime.getModels(id) : all.getBuiltinModels(id)).map((m) => ({
		id: String(m.id),
		name: String(m.name ?? m.id),
		reasoning: Boolean(m.reasoning),
		thinkingLevels: thinking.getSupportedThinkingLevels(m),
		defaultThinkingLevel: thinking.clampThinkingLevel(m, "medium"),
		contextWindow: Number(m.contextWindow ?? 0),
		maxTokens: Number(m.maxTokens ?? 0),
		input: Array.isArray(m.input) ? m.input.map(String) : [],
	}));
	const authMethods = [];
	if (provider.auth?.apiKey?.login) authMethods.push("api_key");
	if (id === "openai-codex" && provider.auth?.oauth) authMethods.push("oauth");
	const keyVars = new Set(envKeys.findEnvKeys(id, anyEnv) ?? []);
	const raw = configured.providers[id];
	if (raw && configValues) {
		const values = [raw.apiKey, ...Object.values(raw.headers ?? {}), ...(raw.models ?? []).flatMap((m) => Object.values(m.headers ?? {})), ...Object.values(raw.modelOverrides ?? {}).flatMap((m) => Object.values(m.headers ?? {}))];
		for (const value of values) if (typeof value === "string") for (const name of configValues.getConfigValueEnvVarNames(value)) keyVars.add(name);
	}
	providers.push({
		id,
		source: raw ? "config" : "builtin",
		authMethods,
		apiKeyEnvVars: [...keyVars].sort(),
		models,
	});
}

const generatedAt = all.getBuiltinModelDataGeneratedAt();
process.stdout.write(JSON.stringify({
	piAIVersion: pkg.version,
	generatedAt: typeof generatedAt === "number" ? new Date(generatedAt).toISOString() : null,
	providers,
	providerConfigs: configured.providers,
}));
