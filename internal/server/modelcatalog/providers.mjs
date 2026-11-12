// 用固定版本 Pi 自己的扩展加载器加载插件的 Pi 扩展，读出它们注册的模型提供方（离线）。
// 用法：node providers.mjs <Pi 主程序目录> <pi-ai 目录> <扩展文件>...
// 由服务端以空环境、临时 HOME 与 PI_CODING_AGENT_DIR、PI_OFFLINE=1 运行；扩展加载时的副作用只落在临时目录。
// 输出一行 JSON：{ piVersion, providers: [...], errors: [{ path, error }] }，扩展原文的错误截断后返回。
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [agentDir, aiDir, ...extensions] = process.argv.slice(2);
if (!agentDir || !aiDir || extensions.length === 0) {
	console.error("缺少 Pi 主程序目录、pi-ai 目录或扩展文件参数");
	process.exit(2);
}

const load = (dir, ...parts) => import(pathToFileURL(join(dir, "dist", ...parts)).href);
const pkg = JSON.parse(readFileSync(join(agentDir, "package.json"), "utf8"));
const { loadExtensions } = await load(agentDir, "core", "extensions", "index.js");
const { ModelRuntime } = await load(agentDir, "core", "model-runtime.js");
const thinking = await load(aiDir, "models.js");

const clip = (value, max) => {
	const text = String(value ?? "");
	return text.length > max ? text.slice(0, max) + "…" : text;
};

const home = process.env.HOME;
const loaded = await loadExtensions(extensions, home);
const errors = loaded.errors.map((e) => ({ path: e.path, error: clip(e.error, 2000) }));
const runtime = await ModelRuntime.create({
	authPath: join(home, "auth.json"),
	modelsPath: null,
	refreshOnCreate: false,
	allowModelNetwork: false,
});
const builtin = new Set(runtime.getProviders().map((p) => String(p.id)));
const providers = [];
for (const { name, config, extensionPath } of loaded.runtime.pendingProviderRegistrations) {
	const id = String(name);
	const entry = {
		id,
		name: clip(config?.name ?? id, 200),
		extension: String(extensionPath),
		oauth: Boolean(config?.oauth),
		apiKey: typeof config?.apiKey === "string" && config.apiKey !== "",
		overridesBuiltin: builtin.has(id),
		error: null,
		models: [],
	};
	try {
		runtime.registerProvider(id, config);
		// 覆盖内置提供方的不进目录，不必列模型。
		if (entry.overridesBuiltin) {
			providers.push(entry);
			continue;
		}
		entry.models = runtime.getModels(id).map((m) => ({
			id: String(m.id),
			name: String(m.name ?? m.id),
			reasoning: Boolean(m.reasoning),
			thinkingLevels: thinking.getSupportedThinkingLevels(m),
			defaultThinkingLevel: thinking.clampThinkingLevel(m, "medium"),
			contextWindow: Number(m.contextWindow ?? 0),
			maxTokens: Number(m.maxTokens ?? 0),
			input: Array.isArray(m.input) ? m.input.map(String) : [],
		}));
	} catch (error) {
		entry.error = clip(error instanceof Error ? error.message : error, 2000);
	}
	providers.push(entry);
}
for (const { provider, extensionPath } of loaded.runtime.pendingNativeProviderRegistrations) {
	errors.push({ path: String(extensionPath), error: `原生提供方 ${clip(provider?.id, 100)} 暂不支持` });
}

// 扩展可能在加载时起定时器或连接；输出写完即退出。
process.stdout.write(JSON.stringify({ piVersion: pkg.version, providers, errors }), () => process.exit(0));
