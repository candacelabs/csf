const requestCharacters = 65_536;
const pendingCodeUnits = requestCharacters * 4;

// Send immediately, then coalesce only input that arrives during a request.
// A failed request may already have reached the shell: never replay it.
export function terminalInput(send: (data: string) => Promise<void>, onFailure: (cause: unknown) => void) {
  let pending = "";
  let sending = false;
  let closed = false;

  function pause(cause: unknown) {
    closed = true;
    pending = "";
    onFailure(cause);
  }

  async function drain() {
    sending = true;
    try {
      while (!closed && pending !== "") {
        const characters = Array.from(pending);
        const data = characters.slice(0, requestCharacters).join("");
        pending = characters.slice(requestCharacters).join("");
        await send(data);
      }
    } catch (cause) {
      if (!closed) pause(cause);
    } finally {
      sending = false;
    }
  }

  return {
    write(data: string) {
      if (closed) return;
      if (pending.length + data.length > pendingCodeUnits) {
        pause(new Error("The terminal input buffer is full"));
        return;
      }
      pending += data;
      if (!sending) void drain();
    },
    close() {
      closed = true;
      pending = "";
    },
  };
}
