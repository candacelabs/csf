import { PairShare, defaultPairOptions, readSessionEvents } from "./pair-server.mjs";

export async function registerPairExtension(joinSession, options = {}) {
  const pendingEvents = [];
  let share;
  let session;

  const command = {
    name: "pair",
    description: "Share this Copilot session with trusted peers in a browser",
    handler: async ({ args }) => {
      const action = (args ?? "").trim().toLowerCase() || "status";
      switch (action) {
        case "start": {
          const status = await share.start();
          await session.log(
            `Pair session is live at ${status.link} Anyone who can reach the link has full control.`,
            { level: "info" },
          );
          return;
        }
        case "stop":
          await share.stop();
          await session.log("Pair session stopped.", { level: "info" });
          return;
        case "status": {
          const status = share.status();
          const message = status.running
            ? `Pair session: ${status.link} (${status.connectedClients} connected)`
            : "Pair session is stopped. Run /pair start to share it.";
          await session.log(message, { level: "info" });
          return;
        }
        default:
          await session.log("Usage: /pair start | /pair status | /pair stop", {
            level: "warning",
          });
      }
    },
  };

  session = await joinSession({
    streaming: true,
    commands: [command],
    onEvent: (event) => {
      if (share) {
        share.ingest(event);
      } else {
        pendingEvents.push(event);
      }
    },
  });

  const pairOptions = {
    ...defaultPairOptions(options.environment),
    ...options.pairOptions,
  };
  share = options.createShare
    ? options.createShare(session, pairOptions)
    : new PairShare(session, pairOptions);

  share.merge(await readSessionEvents(session));
  for (const event of pendingEvents) {
    share.ingest(event);
  }

  return {
    session,
    share,
    async dispose() {
      await share.stop();
    },
  };
}
