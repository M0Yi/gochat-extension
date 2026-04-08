import { defineSetupPluginEntry } from "openclaw/plugin-sdk/core";
import { gochatPlugin } from "./src/channel.js";

export default defineSetupPluginEntry(gochatPlugin);
