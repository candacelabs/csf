import type { ReactNode } from "react";
import { MantineProvider } from "@mantine/core";
import { render } from "@testing-library/react";
import { workbenchTheme } from "./theme";

export function renderWithMantine(ui: ReactNode) {
  return render(ui, {
    wrapper: ({ children }) => (
      <MantineProvider env="test" theme={workbenchTheme} forceColorScheme="light">
        {children}
      </MantineProvider>
    ),
  });
}
