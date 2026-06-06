// Decision Stack — Root Component
// Offline-first, SQLCipher-encrypted, one-card-at-a-time decision clearing

import React, { useEffect } from 'react';
import { StatusBar } from 'expo-status-bar';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { AppNavigator } from '@navigation/AppNavigator';
import { useAuthStore } from '@stores/authStore';
import { useUIStore } from '@stores/uiStore';
import { GestureHandlerRootView } from 'react-native-gesture-handler';
import { StyleSheet } from 'react-native';

/**
 * Root application component.
 *
 * Architecture:
 * - Zustand stores (no Context API) for all global state
 * - op-sqlite + SQLCipher for encrypted local persistence
 * - Offline-first: all operations work without network
 * - One-card-at-a-time decision clearing flow
 * - Raw email bodies NEVER stored locally
 */
export default function App(): JSX.Element {
  const { hydrate: hydrateAuth, isHydrated: authHydrated } = useAuthStore();
  const { hydrate: hydrateUI, isHydrated: uiHydrated } = useUIStore();

  // Hydrate all stores on mount
  useEffect(() => {
    hydrateAuth();
    hydrateUI();
  }, []);

  const isReady = authHydrated && uiHydrated;

  return (
    <GestureHandlerRootView style={styles.container}>
      <SafeAreaProvider>
        <StatusBar style="auto" />
        {isReady && <AppNavigator />}
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
  },
});
