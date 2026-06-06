// Decision Stack — Push Notification Handler (FCM / APNS)
// Manages remote notifications for new cards, draft ready, and session events

import * as Notifications from 'expo-notifications';
import * as Device from 'expo-device';
import { Platform } from 'react-native';
import Constants from 'expo-constants';
import { useAuthStore } from '@stores/authStore';

// ============================================================================
// NOTIFICATION CATEGORIES
// ============================================================================

export type NotificationCategory =
  | 'new_card'           // New decision card arrived
  | 'draft_ready'        // AI draft generated
  | 'session_invite'     // Invited to sending session
  | 'sync_conflict'      // Server rejected a change
  | 'deadline_warning'   // Card approaching deadline
  | 'batch_summary';     // Daily/weekly summary

export interface DecisionNotification {
  id: string;
  category: NotificationCategory;
  title: string;
  body: string;
  data: {
    card_id?: string;
    draft_id?: string;
    session_id?: string;
    deep_link?: string;
  };
  timestamp: number;
}

// ============================================================================
// SETUP
// ============================================================================

/**
 * Configure notification behavior and request permissions.
 */
export async function setupNotifications(): Promise<boolean> {
  // Configure how notifications present
  Notifications.setNotificationHandler({
    handleNotification: async () => ({
      shouldShowAlert: true,
      shouldPlaySound: true,
      shouldSetBadge: true,
    }),
  });

  // Request permissions
  const { status: existingStatus } =
    await Notifications.getPermissionsAsync();
  let finalStatus = existingStatus;

  if (existingStatus !== 'granted') {
    const { status } = await Notifications.requestPermissionsAsync({
      ios: {
        allowAlert: true,
        allowBadge: true,
        allowSound: true,
      },
    });
    finalStatus = status;
  }

  if (finalStatus !== 'granted') {
    return false;
  }

  // Set up notification categories with actions
  await Notifications.setNotificationCategoryAsync('new_card', [
    {
      identifier: 'decide_now',
      buttonTitle: 'Decide Now',
      options: {
        isDestructive: false,
        isAuthenticationRequired: false,
        foreground: true,
      },
    },
    {
      identifier: 'dismiss',
      buttonTitle: 'Dismiss',
      options: {
        isDestructive: true,
        isAuthenticationRequired: false,
      },
    },
  ]);

  await Notifications.setNotificationCategoryAsync('draft_ready', [
    {
      identifier: 'review_draft',
      buttonTitle: 'Review',
      options: {
        foreground: true,
      },
    },
    {
      identifier: 'approve_send',
      buttonTitle: 'Approve & Send',
      options: {
        foreground: true,
      },
    },
  ]);

  return true;
}

/**
 * Get the device's push token for server registration.
 */
export async function getPushToken(): Promise<string | null> {
  if (!Device.isDevice) {
    // Simulator — return a development token format
    return null;
  }

  const projectId =
    Constants.expoConfig?.extra?.eas?.projectId ??
    Constants.easConfig?.projectId;

  try {
    const tokenData = await Notifications.getExpoPushTokenAsync({
      projectId,
    });
    return tokenData.data;
  } catch {
    return null;
  }
}

/**
 * Register push token with the Decision Stack backend.
 */
export async function registerPushTokenWithServer(): Promise<void> {
  const pushToken = await getPushToken();
  if (!pushToken) return;

  const { tokens } = useAuthStore.getState();
  if (!tokens) return;

  const api = require('./api');
  try {
    await api.api.post('/devices/push-token', {
      token: pushToken,
      platform: Platform.OS,
    });
  } catch {
    // Silently fail — push notifications are best-effort
  }
}

// ============================================================================
// LOCAL NOTIFICATIONS
// ============================================================================

/**
 * Schedule a local notification (for offline reminders).
 */
export async function scheduleLocalNotification(
  notification: Omit<DecisionNotification, 'id' | 'timestamp'>,
  trigger?: Notifications.NotificationTriggerInput
): Promise<string> {
  const id = await Notifications.scheduleNotificationAsync({
    content: {
      title: notification.title,
      body: notification.body,
      data: notification.data,
      sound: true,
      badge: 1,
    },
    trigger: trigger ?? { seconds: 1 },
  });
  return id;
}

/**
 * Present an immediate local notification.
 */
export async function presentLocalNotification(
  notification: Omit<DecisionNotification, 'id' | 'timestamp'>
): Promise<void> {
  await Notifications.presentNotificationAsync({
    content: {
      title: notification.title,
      body: notification.body,
      data: notification.data,
      sound: true,
    },
    trigger: null,
  });
}

// ============================================================================
// NOTIFICATION HANDLING
// ============================================================================

/**
 * Parse a received notification into our internal format.
 */
export function parseNotification(
  notification: Notifications.Notification
): DecisionNotification {
  const content = notification.request.content;
  return {
    id: notification.request.identifier,
    category: (content.data?.category as NotificationCategory) ?? 'new_card',
    title: content.title ?? 'Decision Stack',
    body: content.body ?? '',
    data: (content.data ?? {}) as DecisionNotification['data'],
    timestamp: notification.date,
  };
}

/**
 * Set up global notification listeners.
 */
export function addNotificationListeners(handlers: {
  onNotificationReceived?: (notification: DecisionNotification) => void;
  onNotificationTapped?: (notification: DecisionNotification) => void;
}): {
  subscription: Notifications.Subscription;
  responseSubscription: Notifications.Subscription;
} {
  // Foreground notification received
  const subscription = Notifications.addNotificationReceivedListener(
    (notification) => {
      const parsed = parseNotification(notification);
      handlers.onNotificationReceived?.(parsed);
    }
  );

  // User tapped notification
  const responseSubscription =
    Notifications.addNotificationResponseReceivedListener((response) => {
      const parsed = parseNotification(response.notification);
      handlers.onNotificationTapped?.(parsed);
    });

  return { subscription, responseSubscription };
}

/**
 * Remove notification listeners.
 */
export function removeNotificationListeners(listeners: {
  subscription: Notifications.Subscription;
  responseSubscription: Notifications.Subscription;
}): void {
  Notifications.removeNotificationSubscription(listeners.subscription);
  Notifications.removeNotificationSubscription(listeners.responseSubscription);
}

// ============================================================================
// BADGE MANAGEMENT
// ============================================================================

/**
 * Set the app badge count.
 */
export async function setBadgeCount(count: number): Promise<void> {
  await Notifications.setBadgeCountAsync(Math.max(0, count));
}

/**
 * Clear all notifications and badge.
 */
export async function clearAllNotifications(): Promise<void> {
  await Notifications.dismissAllNotificationsAsync();
  await setBadgeCount(0);
}
