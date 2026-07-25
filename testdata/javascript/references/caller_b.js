function wrapper(targetFunc) {
  const value = targetFunc;
  return value();
}
