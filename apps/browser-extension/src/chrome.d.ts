declare const chrome: {
  action: {
    onClicked: {
      addListener(listener: (tab: { id?: number; windowId?: number }) => void): void;
    };
  };
  commands: {
    onCommand: {
      addListener(listener: (command: string) => void): void;
    };
  };
  runtime: {
    id: string;
    lastError?: { message?: string };
    getURL(path: string): string;
    onMessage: {
      addListener(
        listener: (
          message: unknown,
          sender: { tab?: { id?: number; windowId?: number; url?: string } },
          sendResponse: (response?: unknown) => void,
        ) => boolean | void,
      ): void;
    };
    sendMessage(message: unknown): Promise<unknown>;
  };
  scripting: {
    executeScript(details: { target: { tabId: number }; files: string[] }): Promise<void>;
  };
  storage: {
    local: {
      get(keys?: string[] | Record<string, unknown> | string | null): Promise<Record<string, unknown>>;
      set(items: Record<string, unknown>): Promise<void>;
      remove(keys: string | string[]): Promise<void>;
    };
  };
  tabs: {
    query(queryInfo: { active?: boolean; currentWindow?: boolean }): Promise<Array<{ id?: number; windowId?: number }>>;
    sendMessage(tabId: number, message: unknown): Promise<unknown>;
    captureVisibleTab(windowId: number, options?: { format?: "png" | "jpeg"; quality?: number }): Promise<string>;
    create(createProperties: { url: string }): Promise<void>;
  };
};
