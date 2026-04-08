import { createPluginRuntimeStore } from "openclaw/plugin-sdk/runtime-store";
import type { PluginRuntime } from "../../runtime-api.js";
import type { ResolvedGoChatAccount } from "../accounts.js";

type GoChatMonitorState = {
  runtime: PluginRuntime;
  account: ResolvedGoChatAccount;
};

const { setRuntime: setGoChatMonitorState, getRuntime: getGoChatMonitorState } =
  createPluginRuntimeStore<GoChatMonitorState>("GoChat monitor not initialized");
export { setGoChatMonitorState, getGoChatMonitorState };
