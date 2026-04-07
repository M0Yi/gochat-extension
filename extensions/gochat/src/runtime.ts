import { createPluginRuntimeStore } from "openclaw/plugin-sdk/runtime-store";
import type { PluginRuntime } from "../runtime-api.js";

const { setRuntime: _setGoChatRuntime, getRuntime: getGoChatRuntime } =
  createPluginRuntimeStore<PluginRuntime>("GoChat runtime not initialized");

export { getGoChatRuntime };

export function setGoChatRuntime(runtime: PluginRuntime): void {
  _setGoChatRuntime(runtime);
}
