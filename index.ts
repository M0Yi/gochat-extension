import { defineChannelPluginEntry } from "openclaw/plugin-sdk/core";
import { gochatPlugin } from "./src/channel.js";
import { setGoChatRuntime } from "./src/runtime.js";
import { createGoChatTaskTool } from "./src/task-tools.js";
import { resolveGoChatAccount } from "./src/accounts.js";
import type { CoreConfig } from "./src/types.js";

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
        const gochatCmd = program
          .command("gochat")
          .description("GoChat custom backend management");

        gochatCmd
          .command("show-credentials")
          .description("Display connection ID and secret key for GoChat")
          .option("-a, --account <accountId>", "Account ID (default: default account)")
          .action(async (options) => {
            const core = api.getCore();
            const cfg = core.config.loadConfig() as CoreConfig;
            const accountId = options.account || undefined;
            
            try {
              const account = resolveGoChatAccount({ cfg, accountId });
              
              console.log("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━");
              console.log("  GoChat Connection Credentials");
              console.log("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n");
              console.log(`  Account ID:      ${account.accountId}`);
              console.log(`  Mode:            ${account.mode}`);
              console.log(`  Status:          ${account.enabled ? '✓ Enabled' : '✗ Disabled'}`);
              console.log(`  Secret Source:   ${account.secretSource}`);
              console.log("");
              
              if (account.mode === "relay") {
                console.log("  Relay Configuration:");
                console.log(`    Channel ID:    ${account.channelId || "(not set)"}`);
                console.log(`    Relay URL:     ${account.relayPlatformUrl}`);
                console.log(`    Secret Key:    ${account.secret ? account.secret.substring(0, 20) + "..." : "(not set)"}`);
              } else {
                console.log("  Local Configuration:");
                console.log(`    Host:          ${account.directHost}`);
                console.log(`    Port:          ${account.directPort}`);
                console.log(`    Secret Key:    ${account.secret ? account.secret.substring(0, 20) + "..." : "(auto-generated)"}`);
              }
              
              console.log("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n");
            } catch (error) {
              console.error("\n✗ Error retrieving credentials:");
              console.error(`  ${error instanceof Error ? error.message : String(error)}`);
              console.error("");
              process.exit(1);
            }
          });

        program
          .command("gochat")
          .description("GoChat custom backend management");
      },
      {
        descriptors: [
          {
            name: "gochat",
            description: "GoChat custom backend management",
            hasSubcommands: true,
          },
          {
            name: "gochat show-credentials",
            description: "Display connection ID and secret key",
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
