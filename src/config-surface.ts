import { GoChatConfigSchema } from "./config-schema.js";
import { buildChannelConfigSchema } from "openclaw/plugin-sdk/core";

export const GoChatChannelConfigSchema = buildChannelConfigSchema(GoChatConfigSchema);
