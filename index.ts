import { defineChannelPluginEntry } from "openclaw/plugin-sdk/core";
import { gochatPlugin } from "./src/channel.js";
import { setGoChatRuntime } from "./src/runtime.js";
import { createGoChatTaskTool } from "./src/task-tools.js";
import { resolveGoChatAccount } from "./src/accounts.js";
import type { CoreConfig } from "./src/types.js";
import { DEFAULT_MODE_SWITCH_AUTH_TTL_MINUTES, grantGoChatModeSwitchAuthorization } from "./src/mode-switch-authorization.js";
import { ensureGoChatGatewayAccess } from "./src/gateway-access.js";
import { loadConfig, writeConfigFile } from "openclaw/plugin-sdk/config-runtime";

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
            hasSubcommands: true,
          },
        ],
      },
    );
  },
  registerFull(api) {
    api.registerTool(createGoChatTaskTool(), {
      name: "gochat_tasks",
    });

    api.registerCli(
      ({ program, config: cfg }) => {
        const gochatCmd = program
          .command("gochat")
          .description("GoChat custom backend management");

        gochatCmd
          .command("show-credentials")
          .description("Display connection ID and secret key for GoChat")
          .option("-a, --account <accountId>", "Account ID (default: default account)")
          .action(async (options) => {
            const accountId = options.account || undefined;

            try {
              const account = resolveGoChatAccount({ cfg: cfg as CoreConfig, accountId });

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
                console.log(`    Secret Key:    ${account.secret || "(not set)"}`);
              } else {
                console.log("  Local Configuration:");
                console.log(`    Host:          ${account.directHost}`);
                console.log(`    Port:          ${account.directPort}`);
                console.log(`    Secret Key:    ${account.secret || "(auto-generated)"}`);
              }

              console.log("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n");
            } catch (error) {
              console.error("\n✗ Error retrieving credentials:");
              console.error(`  ${error instanceof Error ? error.message : String(error)}`);
              console.error("");
              process.exit(1);
            }
          });

        gochatCmd
          .command("authorize-mode-switch")
          .description("Authorize the next explicit mode switch for a GoChat account")
          .requiredOption("--mode <mode>", "Target mode: local or relay")
          .option("-a, --account <accountId>", "Account ID (default: default account)")
          .option("--ttl-minutes <minutes>", "Authorization lifetime in minutes", String(DEFAULT_MODE_SWITCH_AUTH_TTL_MINUTES))
          .option("--json", "Output JSON result")
          .action(async (options) => {
            const rawMode = String(options.mode ?? "").trim().toLowerCase();
            if (rawMode !== "local" && rawMode !== "relay") {
              console.error("\n✗ Invalid mode. Use --mode local or --mode relay.\n");
              process.exit(1);
            }

            const ttlMinutes = Number.parseInt(String(options.ttlMinutes ?? DEFAULT_MODE_SWITCH_AUTH_TTL_MINUTES), 10);
            if (!Number.isFinite(ttlMinutes) || ttlMinutes <= 0) {
              console.error("\n✗ Invalid --ttl-minutes value.\n");
              process.exit(1);
            }

            try {
              const accountId = options.account || undefined;
              const currentCfg = loadConfig() as CoreConfig;
              const nextCfg = grantGoChatModeSwitchAuthorization({
                cfg: currentCfg,
                accountId: accountId ?? "default",
                targetMode: rawMode,
                ttlMinutes,
              });
              await writeConfigFile(nextCfg as Parameters<typeof writeConfigFile>[0]);

              const expiresAt = new Date(Date.now() + ttlMinutes * 60_000).toISOString();
              if (options.json) {
                console.log(JSON.stringify({
                  accountId: accountId ?? "default",
                  targetMode: rawMode,
                  ttlMinutes,
                  expiresAt,
                }, null, 2));
                return;
              }

              console.log(
                `Authorized next GoChat mode switch to ${rawMode} for account ${accountId ?? "default"} until ${expiresAt}.`,
              );
            } catch (error) {
              console.error("\n✗ Failed to authorize mode switch:");
              console.error(`  ${error instanceof Error ? error.message : String(error)}`);
              console.error("");
              process.exit(1);
            }
          });

        gochatCmd
          .command("ensure-gateway-access")
          .description("Manually normalize local gateway routing and approve safe local CLI repair requests")
          .option("--json", "Output JSON result")
          .action(async (options) => {
            try {
              const result = await ensureGoChatGatewayAccess({
                logger: {
                  info: (message) => console.error(message),
                  warn: (message) => console.error(message),
                  error: (message) => console.error(message),
                },
              });

              if (options.json) {
                console.log(JSON.stringify(result, null, 2));
                return;
              }

              if (result.normalizedGatewayRemoteUrlTo) {
                console.log(
                  `Normalized gateway.remote.url: ${result.normalizedGatewayRemoteUrlFrom} -> ${result.normalizedGatewayRemoteUrlTo}`,
                );
              }
              if (result.approvedRequestId) {
                console.log(
                  `Approved local CLI repair request: ${result.approvedRequestId}${result.approvedDeviceId ? ` (device ${result.approvedDeviceId})` : ""}`,
                );
              }
              if (!result.normalizedGatewayRemoteUrlTo && !result.approvedRequestId) {
                console.log(result.skippedReason || "No gateway access changes were needed.");
              } else if (result.skippedReason) {
                console.log(`Skipped: ${result.skippedReason}`);
              }
            } catch (error) {
              console.error("\n✗ Failed to ensure gateway access:");
              console.error(`  ${error instanceof Error ? error.message : String(error)}`);
              console.error("");
              process.exit(1);
            }
          });
      },
      {
        descriptors: [
          {
            name: "gochat show-credentials",
            description: "Display connection ID and secret key",
            hasSubcommands: false,
          },
          {
            name: "gochat authorize-mode-switch",
            description: "Authorize the next GoChat mode switch",
            hasSubcommands: false,
          },
          {
            name: "gochat ensure-gateway-access",
            description: "Manually normalize loopback gateway routing and approve safe local CLI repair requests",
            hasSubcommands: false,
          },
        ],
      },
    );
  },
});
