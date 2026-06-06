// ============================================================================
// Background Sync — expo-background-fetch integration
// ============================================================================
// Registers a 15-minute background task that drains the sync queue even
// when the app is not in the foreground.
//
// Invariants:
// - Only runs when the device is online (checked via @react-native-community/netinfo)
// - Returns BackgroundFetch.Result.NoData when there is nothing to sync
// - Returns BackgroundFetch.Result.NewData when changes were uploaded/downloaded
// - Idempotent: the sync engine itself guards against duplicate application
// ============================================================================

import * as BackgroundFetch from "expo-background-fetch";
import * as TaskManager from "expo-task-manager";
import NetInfo from "@react-native-community/netinfo";
import { SyncEngine } from "./sync";

// ---------------------------------------------------------------------------
// Task name — must be unique within the app
// ---------------------------------------------------------------------------

export const BACKGROUND_SYNC_TASK = "DECISION_STACK_SYNC";

// ---------------------------------------------------------------------------
// Interval configuration
// ---------------------------------------------------------------------------

const MIN_INTERVAL_MS = 15 * 60 * 1000; // 15 minutes

// ---------------------------------------------------------------------------
// Task definition
// ---------------------------------------------------------------------------

TaskManager.defineTask(BACKGROUND_SYNC_TASK, async () => {
  try {
    // 1. Check network connectivity
    const netInfo = await NetInfo.fetch();
    if (!netInfo.isConnected) {
      return BackgroundFetch.BackgroundFetchResult.NoData;
    }

    // 2. Run sync
    const syncEngine = new SyncEngine();
    const result = await syncEngine.sync();

    // 3. Return appropriate result so iOS/Android can throttle intelligently
    if (result.uploaded > 0 || result.newCards > 0 || result.updatedCards > 0) {
      return BackgroundFetch.BackgroundFetchResult.NewData;
    }

    return BackgroundFetch.BackgroundFetchResult.NoData;
  } catch (error) {
    // Log but don't throw — we don't want to crash the background task
    const message = error instanceof Error ? error.message : String(error);
    console.error(`[BackgroundSync] Task failed: ${message}`);
    return BackgroundFetch.BackgroundFetchResult.Failed;
  }
});

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

/**
 * Register the background sync task with the OS.
 * Call this once after app launch (e.g., in your root layout effect).
 *
 * Idempotent: calling multiple times is safe — Expo deduplicates by task name.
 */
export async function registerBackgroundSync(): Promise<void> {
  // Check if the task is already registered
  const isRegistered = await TaskManager.isTaskRegisteredAsync(BACKGROUND_SYNC_TASK);
  if (isRegistered) {
    return;
  }

  await BackgroundFetch.registerTaskAsync(BACKGROUND_SYNC_TASK, {
    minimumInterval: MIN_INTERVAL_MS / 1000, // Expo expects seconds
    stopOnTerminate: false,   // Continue after app kill (best-effort on iOS)
    startOnBoot: true,        // Restart after device reboot (Android)
  });

  console.log(`[BackgroundSync] Registered task "${BACKGROUND_SYNC_TASK}" (${MIN_INTERVAL_MS / 1000}s interval)`);
}

/**
 * Unregister the background task. Use this when the user signs out.
 */
export async function unregisterBackgroundSync(): Promise<void> {
  const isRegistered = await TaskManager.isTaskRegisteredAsync(BACKGROUND_SYNC_TASK);
  if (!isRegistered) {
    return;
  }

  await BackgroundFetch.unregisterTaskAsync(BACKGROUND_SYNC_TASK);
  console.log(`[BackgroundSync] Unregistered task "${BACKGROUND_SYNC_TASK}"`);
}

/**
 * Get the current status of background fetch permissions & availability.
 * Useful for settings UI.
 */
export async function getBackgroundSyncStatus(): Promise<{
  isRegistered: boolean;
  isAvailable: boolean;
  status: BackgroundFetch.BackgroundFetchStatus | null;
}> {
  const isAvailable = await BackgroundFetch.isAvailableAsync();
  const status = isAvailable ? await BackgroundFetch.getStatusAsync() : null;
  const isRegistered = await TaskManager.isTaskRegisteredAsync(BACKGROUND_SYNC_TASK);

  return { isRegistered, isAvailable, status };
}
