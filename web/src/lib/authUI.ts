import { createContext, useContext } from "react";

/** UI-only: whether the API-key dialog is covering the app. */
export type AuthUIState = {
  dialogOpen: boolean;
};

export const AuthUIContext = createContext<AuthUIState>({ dialogOpen: false });

export function useAuthUI(): AuthUIState {
  return useContext(AuthUIContext);
}
