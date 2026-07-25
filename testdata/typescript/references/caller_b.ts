function wrapper(targetFunc: () => void): void {
  const value = targetFunc;
  return value();
}
