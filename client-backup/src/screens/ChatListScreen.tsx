// Decision Stack — Chat List Screen
// iMessage/WhatsApp-style conversation list with FAB for new chat

import React, { useCallback } from 'react';
import {
  View,
  Text,
  FlatList,
  TouchableOpacity,
  StyleSheet,
  RefreshControl,
} from 'react-native';
import { useNavigation } from '@react-navigation/native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { palette } from '@theme/colors';
import { fontSize, fontWeight, lineHeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import { useConversations } from '@hooks/useConversations';
import { ConversationCard } from '@components/chat/ConversationCard';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import type { RootStackParamList } from '@navigation/AppNavigator';

type ChatListNavigationProp = NativeStackNavigationProp<
  RootStackParamList,
  'ChatList'
>;

/**
 * ChatListScreen — displays all conversations in a scrollable list.
 * Features:
 * - Pull-to-refresh
 * - Swipe-to-delete on each row
 * - FAB for creating new conversations
 * - Clean, warm palette consistent with app theme
 */
export const ChatListScreen: React.FC = () => {
  const navigation = useNavigation<ChatListNavigationProp>();
  const {
    conversations,
    isLoading,
    loadConversations,
    createConversation,
    deleteConversation,
  } = useConversations();

  const handlePressConversation = useCallback(
    (id: string) => {
      navigation.navigate('Chat', { conversationId: id });
    },
    [navigation]
  );

  const handleNewChat = useCallback(async () => {
    const conv = await createConversation();
    if (conv) {
      navigation.navigate('Chat', { conversationId: conv.id });
    }
  }, [createConversation, navigation]);

  const handleDeleteConversation = useCallback(
    (id: string) => {
      deleteConversation(id);
    },
    [deleteConversation]
  );

  const renderItem = useCallback(
    ({ item }: { item: (typeof conversations)[0] }) => (
      <ConversationCard
        conversation={item}
        onPress={handlePressConversation}
        onDelete={handleDeleteConversation}
      />
    ),
    [handlePressConversation, handleDeleteConversation]
  );

  return (
    <SafeAreaView style={styles.container} edges={['top']}>
      {/* Header */}
      <View style={styles.header}>
        <View>
          <Text style={styles.headerTitle}>Messages</Text>
          <Text style={styles.headerSubtitle}>
            {conversations.length}{' '}
            {conversations.length === 1 ? 'conversation' : 'conversations'}
          </Text>
        </View>
      </View>

      {/* Conversation List */}
      <FlatList
        data={conversations}
        renderItem={renderItem}
        keyExtractor={(item) => item.id}
        refreshControl={
          <RefreshControl
            refreshing={isLoading}
            onRefresh={loadConversations}
            tintColor={palette.sand[400]}
            colors={[palette.sand[400]]}
          />
        }
        contentContainerStyle={
          conversations.length === 0 ? styles.emptyContent : styles.listContent
        }
        ListEmptyComponent={
          <View style={styles.emptyState}>
            <Text style={styles.emptyEmoji}>💬</Text>
            <Text style={styles.emptyTitle}>No conversations yet</Text>
            <Text style={styles.emptySubtitle}>
              Tap the button below to start chatting about your email decisions.
            </Text>
          </View>
        }
      />

      {/* FAB — New Chat */}
      <TouchableOpacity
        onPress={handleNewChat}
        style={styles.fab}
        activeOpacity={0.8}
        accessibilityLabel="Start new chat"
        accessibilityRole="button"
      >
        <Text style={styles.fabIcon}>+</Text>
      </TouchableOpacity>
    </SafeAreaView>
  );
};

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: palette.ink[50],
  },
  header: {
    paddingHorizontal: spacing[5],
    paddingVertical: spacing[4],
    backgroundColor: '#ffffff',
    borderBottomWidth: 1,
    borderBottomColor: palette.ink[100],
  },
  headerTitle: {
    fontSize: fontSize['3xl'],
    fontWeight: fontWeight.bold,
    color: palette.ink[900],
    letterSpacing: -0.5,
  },
  headerSubtitle: {
    fontSize: fontSize.sm,
    color: palette.ink[500],
    marginTop: spacing[0.5],
  },
  listContent: {
    backgroundColor: '#ffffff',
  },
  emptyContent: {
    flex: 1,
    justifyContent: 'center',
  },
  emptyState: {
    alignItems: 'center',
    paddingHorizontal: spacing[10],
    paddingVertical: spacing[16],
  },
  emptyEmoji: {
    fontSize: 48,
    marginBottom: spacing[4],
  },
  emptyTitle: {
    fontSize: fontSize.xl,
    fontWeight: fontWeight.semibold,
    color: palette.ink[700],
    marginBottom: spacing[2],
  },
  emptySubtitle: {
    fontSize: fontSize.base,
    color: palette.ink[500],
    textAlign: 'center',
    lineHeight: lineHeight.normal * fontSize.base,
  },
  fab: {
    position: 'absolute',
    right: spacing[5],
    bottom: spacing[8],
    width: spacing[14],
    height: spacing[14],
    borderRadius: spacing[7],
    backgroundColor: palette.sand[400],
    justifyContent: 'center',
    alignItems: 'center',
    shadowColor: palette.ink[900],
    shadowOffset: { width: 0, height: 4 },
    shadowRadius: 12,
    shadowOpacity: 0.2,
    elevation: 8,
  },
  fabIcon: {
    fontSize: fontSize['2xl'],
    color: '#ffffff',
    fontWeight: fontWeight.semibold,
  },
});
