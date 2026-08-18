import {
  fail as dependencyFail,
  greet,
  request as dependencyRequest,
} from "fixture-dependency";

export async function request(url: string): Promise<Response> {
  return dependencyRequest(url);
}

export function fail(): never {
  return dependencyFail();
}

console.log(greet("slick"));
