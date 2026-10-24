import { readFileSync } from "node:fs";

// 根目录与前端共用同一版本声明，防止混用 npm/yarn 重新生成安装树。
const manifest = JSON.parse(
  readFileSync(new URL("../package.json", import.meta.url), "utf8"),
);
const version = manifest.packageManager.split("@")[1].split("+")[0];
const agent = process.env.npm_config_user_agent ?? "";
if (!agent.startsWith(`pnpm/${version} `)) {
  console.error(
    `本工程使用 pnpm ${version}。请启用 Corepack，并在工程根目录运行 pnpm install。`,
  );
  process.exit(1);
}
