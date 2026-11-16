export function remoteKeyCode(event: KeyboardEvent): number {
  // webOS remotes report Back as numeric key 461; older firmware has no usable key string.
  // eslint-disable-next-line @typescript-eslint/no-deprecated -- Required by the webOS remote event API.
  return event.keyCode;
}
