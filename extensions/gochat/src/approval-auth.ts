import { resolveGoChatAccount } from "./accounts.js";
import type { CoreConfig } from "./types.js";

import { isGoChatSenderAllowed } from "./gochat/auth.js";

import { stripGoChatTargetPrefix } from "./normalize.js";

