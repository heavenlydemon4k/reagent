// Decision Stack — Stack Navigator
// BatchGate → CardStack → DraftReview flow for one-card-at-a-time clearing

import React, { useEffect } from 'react';
import { NavigationContainer } from '@react-navigation/native';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { useAuthStore } from '@stores/authStore';
import { useUIStore } from '@stores/uiStore';
import { useAuth } from '@hooks/useAuth';
import { useSync } from '@hooks/useSync';
import { useCards } from '@hooks/useCards';
import { useTheme } from '@hooks/useTheme';
import { ActivityIndicator, View, Text, StyleSheet } from 'react-native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';

// ── Chat Screens ─────────────────────────────────────────────────────────────
import { ChatListScreen } from '@screens/ChatListScreen';
import { ChatScreen } from '@screens/ChatScreen';
import { ChatVoiceScreen } from '@screens/ChatVoiceScreen';

// ── Decision Screens ─────────────────────────────────────────────────────────
import { DecisionInputScreen } from '@screens/DecisionInputScreen';
import { ContactProfileScreen } from '@screens/ContactProfileScreen';
import { useNavigation, useRoute, type RouteProp } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import type { DecisionCard } from '../types/cards';

// ============================================================================
// PLACEHOLDER SCREENS (to be implemented by UI track)
// ============================================================================

const BatchGateScreen: React.FC = () => {
  const { loadCards, pendingCount, hasCards } = useCards();
  const sync = useSync();
  const { colors } = useTheme();

  useEffect(() => {
    loadCards();
  }, []);

  return (
    <View style={[styles.centered, { backgroundColor: colors.background }]}>
      <Text style={[styles.title, { color: colors.textPrimary }]}>Decision Stack</Text>
      <Text style={[styles.subtitle, { color: colors.textSecondary }]}>
        {hasCards
          ? `${pendingCount} decisions waiting`
          : sync.isOnline
          ? 'Fetching your decisions...'
          : 'Waiting for network...'}
      </Text>
      {!sync.isOnline && (
        <Text style={[styles.hint, { color: colors.textTertiary }]}>
          Offline mode — your decisions will sync when you reconnect
        </Text>
      )}
      {sync.isSyncing && (
        <ActivityIndicator
          style={styles.spinner}
          color={colors.primary}
        />
      )}
    </View>
  );
};

const CardStackScreen: React.FC = () => {
  const { currentCard, pendingCount, nextCard, skipCard, decide } = useCards();
  const { colors } = useTheme();

  return (
    <View style={[styles.container, { backgroundColor: colors.background }]}>
      <View style={styles.header}>
        <Text style={[styles.count, { color: colors.textSecondary }]}>
          {pendingCount} remaining
        </Text>
      </View>

      {currentCard ? (
        <View style={[styles.card, { backgroundColor: colors.surface, borderColor: colors.border }]}>
          <Text style={[styles.fromName, { color: colors.textPrimary }]}>
            {currentCard.from.name}
          </Text>
          <Text style={[styles.fromEmail, { color: colors.textSecondary }]}>
            {currentCard.from.email}
          </Text>
          <Text style={[styles.theyWant, { color: colors.textPrimary }]}>
            {currentCard.they_want}
          </Text>
          <Text style={[styles.need, { color: colors.textSecondary }]}>
            {currentCard.need_from_user}
          </Text>
        </View>
      ) : (
        <View style={styles.empty}>
          <Text style={[styles.emptyText, { color: colors.textTertiary }]}>
            All caught up!
          </Text>
        </View>
      )}
    </View>
  );
};

const DraftReviewScreen: React.FC = () => {
  const { colors } = useTheme();

  return (
    <View style={[styles.centered, { backgroundColor: colors.background }]}>
      <Text style={[styles.title, { color: colors.textPrimary }]}>Review Draft</Text>
      <Text style={[styles.subtitle, { color: colors.textSecondary }]}>Draft review placeholder</Text>
    </View>
  );
};

const SourceViewerScreen: React.FC = () => {
  const { colors } = useTheme();

  return (
    <View style={[styles.centered, { backgroundColor: colors.background }]}>
      <Text style={[styles.title, { color: colors.textPrimary }]}>Source Viewer</Text>
      <Text style={[styles.subtitle, { color: colors.textSecondary }]}>Source reference placeholder</Text>
    </View>
  );
};

/**
 * DecisionInputScreen wrapper — adapts React Navigation route/params
 * to the screen's expected { card, onDraftReady, onCancel } props.
 */
const DecisionInputScreenNavWrapper: React.FC = () => {
  type NavProp = NativeStackNavigationProp<RootStackParamList>;
  const navigation = useNavigation<NavProp>();
  const route = useRoute<RouteProp<RootStackParamList, 'DecisionInput'>>();
  const { card } = route.params;

  return (
    <DecisionInputScreen
      card={card}
      onDraftReady={(draft) =>
        navigation.navigate('DraftReview', {
          draftId: draft?.id,
          cardId: card.id,
        })
      }
      onCancel={() => navigation.goBack()}
    />
  );
};

/**
 * ContactProfileScreen wrapper — adapts React Navigation route/params
 * to the screen's expected props. Provides back navigation and action handlers.
 */
const ContactProfileScreenNavWrapper: React.FC = () => {
  type NavProp = NativeStackNavigationProp<RootStackParamList>;
  const navigation = useNavigation<NavProp>();
  const route = useRoute<RouteProp<RootStackParamList, 'ContactProfile'>>();
  const { contactId, contactName, contactEmail } = route.params;

  return (
    <ContactProfileScreen
      contactId={contactId}
      contactName={contactName}
      contactEmail={contactEmail}
      onBack={() => navigation.goBack()}
      onPressThread={(threadId) => {
        // Navigate to source viewer or thread detail
        navigation.navigate('SourceViewer', {
          sourceName: threadId,
        });
      }}
      onSendEmail={(email) => {
        // Open email composer — handled by linking
      }}
      onScheduleMeeting={(cid) => {
        // Open calendar picker
      }}
      onMuteContact={(cid) => {
        // Mute contact via API
      }}
    />
  );
};

// ============================================================================
// NAVIGATION TYPES
// ============================================================================

export type RootStackParamList = {
  BatchGate: undefined;
  CardStack: undefined;
  DecisionInput: { card: DecisionCard };
  DraftReview: { draftId?: string; cardId?: string };
  SourceViewer: { sourceUrl?: string; sourceName?: string };
  ChatList: undefined;
  Chat: { conversationId?: string; linkedCardId?: string };
  ChatVoice: { conversationId: string };
  ContactProfile: { contactId: string; contactName?: string; contactEmail?: string };
};

const Stack = createNativeStackNavigator<RootStackParamList>();

// ============================================================================
// AUTH GATE
// ============================================================================

const AuthGate: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { isHydrated, isAuthenticated } = useAuth();
  const { colors } = useTheme();

  if (!isHydrated) {
    return (
      <View style={[styles.centered, { backgroundColor: colors.background }]}>
        <ActivityIndicator size="large" color={colors.primary} />
      </View>
    );
  }

  if (!isAuthenticated) {
    return (
      <View style={[styles.centered, { backgroundColor: colors.background }]}>
        <Text style={[styles.title, { color: colors.textPrimary }]}>Decision Stack</Text>
        <Text style={[styles.subtitle, { color: colors.textSecondary }]}>
          Authentication required
        </Text>
      </View>
    );
  }

  return <>{children}</>;
};

// ============================================================================
// NAVIGATOR
// ============================================================================

export const AppNavigator: React.FC = () => {
  const { isHydrated } = useAuth();
  const uiStore = useUIStore();
  const { colors, isDark } = useTheme();

  // Initialize background sync on mount
  useEffect(() => {
    if (isHydrated) {
      const sync = useSync();
      sync.registerBackground().catch(() => {});
    }
  }, [isHydrated]);

  return (
    <AuthGate>
      <NavigationContainer
        onStateChange={(state) => {
          if (state) {
            const current = state.routes[state.index];
            uiStore.navigateTo(current.name);
          }
        }}
      >
        <Stack.Navigator
          initialRouteName="BatchGate"
          screenOptions={{
            headerShown: false,
            animation: 'slide_from_right',
            contentStyle: { backgroundColor: colors.background },
          }}
        >
          <Stack.Screen
            name="BatchGate"
            component={BatchGateScreen}
            options={{
              gestureEnabled: false,
            }}
          />
          <Stack.Screen
            name="CardStack"
            component={CardStackScreen}
            options={{
              gestureEnabled: false,
            }}
          />
          <Stack.Screen
            name="DecisionInput"
            component={DecisionInputScreenNavWrapper}
            options={{
              presentation: 'modal',
              animation: 'slide_from_bottom',
              gestureEnabled: false,
            }}
          />
          <Stack.Screen
            name="DraftReview"
            component={DraftReviewScreen}
            options={{
              presentation: 'modal',
              animation: 'slide_from_bottom',
            }}
          />
          <Stack.Screen
            name="SourceViewer"
            component={SourceViewerScreen}
            options={{
              presentation: 'modal',
              animation: 'slide_from_bottom',
            }}
          />

          {/* ── Chat Flow ─────────────────────────────────────────── */}
          <Stack.Screen
            name="ChatList"
            component={ChatListScreen}
            options={{
              animation: 'slide_from_right',
            }}
          />
          <Stack.Screen
            name="Chat"
            component={ChatScreen}
            options={{
              animation: 'slide_from_right',
            }}
          />
          <Stack.Screen
            name="ChatVoice"
            component={ChatVoiceScreen}
            options={{
              presentation: 'fullScreenModal',
              animation: 'fade',
            }}
          />

          {/* ── Contact Profile (drill-down) ──────────────────────── */}
          <Stack.Screen
            name="ContactProfile"
            component={ContactProfileScreenNavWrapper}
            options={{
              animation: 'slide_from_right',
            }}
          />
        </Stack.Navigator>
      </NavigationContainer>
    </AuthGate>
  );
};

// ============================================================================
// STYLES
// ============================================================================

const styles = StyleSheet.create({
  centered: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    padding: spacing[6],
  },
  container: {
    flex: 1,
    padding: spacing[4],
  },
  header: {
    paddingVertical: spacing[4],
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
  },
  title: {
    fontSize: fontSize['2xl'],
    fontWeight: fontWeight.semibold,
    marginBottom: spacing[2],
  },
  subtitle: {
    fontSize: fontSize.base,
    textAlign: 'center',
  },
  hint: {
    fontSize: fontSize.sm,
    textAlign: 'center',
    marginTop: spacing[4],
  },
  count: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
  },
  spinner: {
    marginTop: spacing[6],
  },
  card: {
    borderRadius: spacing[3],
    padding: spacing[5],
    borderWidth: 1,
  },
  fromName: {
    fontSize: fontSize.lg,
    fontWeight: fontWeight.semibold,
  },
  fromEmail: {
    fontSize: fontSize.sm,
    marginBottom: spacing[3],
  },
  theyWant: {
    fontSize: fontSize.base,
    lineHeight: 22,
    marginBottom: spacing[3],
  },
  need: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
  },
  empty: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
  },
  emptyText: {
    fontSize: fontSize.xl,
    fontWeight: fontWeight.medium,
  },
});
