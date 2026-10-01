import { joinSession } from "@github/copilot-sdk/extension";

import { registerPairExtension } from "./runtime.mjs";

await registerPairExtension(joinSession);
