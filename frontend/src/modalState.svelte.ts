class ModalState {
  installOpen = $state(false);
  gameSettingsOpen = $state(false);
  launcherSettingsOpen = $state(false);

  get isAnyOpen(): boolean {
    return this.installOpen || this.gameSettingsOpen || this.launcherSettingsOpen;
  }
}

export const modalState = new ModalState();
