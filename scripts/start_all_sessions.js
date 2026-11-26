#!/usr/bin/env node

const API_BASE_URL = "http://127.0.0.1:4010";
const API_KEY = "123456";

async function requestJson(url, apiKey, method = "GET") {
  let response;
  try {
    response = await fetch(url, {
      method,
      headers: {
        "X-API-Key": apiKey,
        Accept: "application/json",
      },
      signal: AbortSignal.timeout(30000),
    });
  } catch (error) {
    throw new Error(`could not reach ${url}: ${error.message}`);
  }

  const body = await response.text();
  if (!response.ok) {
    throw new Error(`HTTP ${response.status} from ${url}: ${body}`);
  }
  try {
    return JSON.parse(body);
  } catch (error) {
    throw new Error(`invalid JSON response from ${url}: ${error.message}`);
  }
}

function parseArgs(args) {
  let dryRun = false;

  for (let index = 0; index < args.length; index += 1) {
    if (args[index] === "--dry-run") {
      dryRun = true;
    } else if (args[index] === "--help" || args[index] === "-h") {
      console.log("Usage: node scripts/start_all_sessions.js [--dry-run]");
      process.exit(0);
    } else {
      throw new Error(`unknown or incomplete argument: ${args[index]}`);
    }
  }

  return { dryRun };
}

async function main() {
  let options;
  try {
    options = parseArgs(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 2;
    return;
  }

  if (!API_KEY || API_KEY === "REPLACE_WITH_YOUR_API_KEY") {
    console.error("Set API_KEY in the constants at the top of this script.");
    process.exitCode = 1;
    return;
  }

  const baseUrl = API_BASE_URL.replace(/\/+$/, "");
  let sessions;
  try {
    const response = await requestJson(`${baseUrl}/sessions`, API_KEY);
    if (!Array.isArray(response.sessions)) {
      throw new Error("GET /sessions returned an invalid response: expected a sessions list");
    }
    sessions = response.sessions;
  } catch (error) {
    console.error(`Failed to list sessions: ${error.message}`);
    process.exitCode = 1;
    return;
  }

  const candidates = sessions.filter(
    (session) =>
      session &&
      typeof session === "object" &&
      session.id &&
      !session.sessionLoaded &&
      session.status !== "initializing",
  );

  if (candidates.length === 0) {
    console.log("No unloaded persisted sessions found.");
    return;
  }

  let failures = 0;
  for (const session of candidates) {
    if (options.dryRun) {
      console.log(`Would start ${session.id}`);
      continue;
    }

    const startUrl = new URL("/startWpSession", `${baseUrl}/`);
    startUrl.searchParams.set("id", session.id);
    try {
      const result = await requestJson(startUrl, API_KEY, "POST");
      console.log(`Started ${session.id}: ${result.status || "unknown status"}`);
    } catch (error) {
      failures += 1;
      console.error(`Failed to start ${session.id}: ${error.message}`);
    }
  }

  if (failures > 0) {
    console.error(`Finished with ${failures} session(s) that failed to start.`);
    process.exitCode = 1;
  } else if (!options.dryRun) {
    console.log(`Start requests completed for ${candidates.length} session(s).`);
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
