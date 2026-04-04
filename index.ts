import { defineChannelPluginEntry } from "openclaw/plugin-sdk/core";
import { gochatPlugin } from "./src/channel.js";
import { setGoChatRuntime } from "./src/runtime.js";
import { createGoChatTaskTool } from "./src/task-tools.js";

export { gochatPlugin } from "./src/channel.js";
export { setGoChatRuntime } from "./src/runtime.js";

export default defineChannelPluginEntry({
  id: "gochat",
  name: "GoChat",
  description: "Custom chat backend via HTTP webhook with Go server",
  plugin: gochatPlugin,
  setRuntime: setGoChatRuntime,
  registerCliMetadata(api) {
    api.registerCli(
      ({ program }) => {
        program
          .command("gochat")
          .description("GoChat custom backend management");
      },
      {
        descriptors: [
          {
            name: "gochat",
            description: "GoChat custom backend management",
            hasSubcommands: false,
          },
        ],
      },
    );
  },
  registerFull(api) {
    api.registerTool(createGoChatTaskTool(), {
      name: "gochat_tasks",
    });
  },
});
