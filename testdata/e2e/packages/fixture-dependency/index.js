export class DependencyError extends Error {}

export function greet(name) {
  return `hello ${name}`;
}

export function request(url) {
  return fetch(url);
}

export function fail() {
  throw new DependencyError("dependency failed");
}
