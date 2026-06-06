// Decision Stack — Main Chat Interface
// Full chat screen with message list, text/voice input, suggested actions, and navigation

import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  KeyboardAvoidingView,
  Platform,
} from 'react-native';
import { useNavigation, useRoute, type RouteProp } from '@react-navigation/native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import { useChat } from '@hooks/useChat';
import { useVoiceChat } from '@hooks/useVoiceChat';
import { useTheme } from '@hooks/useTheme';
import { MessageList } from '@components/chat/MessageList';
import { ChatInput, type InputMode } from '@components/chat/ChatInput';
import { SuggestedAction } from '@components/chat/SuggestedAction';
import { TranscriptionView } from '@components/voice/TranscriptionView';
import { ThemeToggle } from '@components/common/ThemeToggle';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import type { RootStackParamList } from '@navigation/AppNavigator';
import type { ChunkCitation } from '@types/cards';

type ChatScreenRouteProp = RouteProp<RootStackParamList, 'Chat'>;
type ChatScreenNavigationProp = NativeStackNavigationProp<
  RootStackParamList,
  'Chat'
>;

/**
 * ChatScreen — the main conversational interface.
 *
 * Layout:
 * - Header: conversation title + voice toggle + theme toggle
 * - Body: MessageList (scrollable messages, auto-scroll to bottom)
 * - Suggested actions: action chips above input (when present)
 * - Footer: ChatInput (text field + voice button + send)
 *
 * Features:
 * - Voice mode with live transcription and waveform
 * - Suggested actions: "Clear my batch", "What about Sarah?", "Check my calendar"
 * - Citation chips in assistant messages
 * - Audio playback for TTS responses
 * - Theme-aware colors (light/dark mode)
 */
export const ChatScreen: React.FC = () => {
  const navigation = useNavigation<ChatScreenNavigationProp>();
  const route = useRoute<ChatScreenRouteProp>();
  const conversationId = route.params?.conversationId;
  const { colors, isDark } = useTheme();

  // Chat state
  const {
    messages,
    isLoading,
    inputText,
    suggestedAction,
    actionTargetId,
    conversationTitle,
    audioUrl,
    setInputText,
    sendMessage,
    sendVoiceMessage,
    dismissSuggestedAction,
  } = useChat(conversationId);

  // Voice state
  const voiceChat = useVoiceChat();
  const [inputMode, setInputMode] = useState<InputMode>('text');
  const [currentlyPlayingAudio, setCurrentlyPlayingAudio] = useState<
    string | null
  >(null);

  // Handle incoming voice transcription
  const transcriptionRef = useRef(voiceChat.transcription);
  useEffect(() => {
    transcriptionRef.current = voiceChat.transcription;
  }, [voiceChat.transcription]);

  // Auto-play TTS when audioUrl arrives
  useEffect(() => {
    if (audioUrl && inputMode === 'voice') {
      voiceChat.playTTS(audioUrl);
      setCurrentlyPlayingAudio(audioUrl);
    }
  }, [audioUrl, inputMode, voiceChat]);

  // Handle send from text input
  const handleSend = useCallback(
    async (text: string) => {
      await sendMessage(text);
    },
    [sendMessage]
  );

  // Handle voice recording stop
  const handleStopRecording = useCallback(async () => {
    const transcription = await voiceChat.stopRecording();
    if (transcription && transcription.trim()) {
      await sendMessage(transcription.trim());
    }
    setInputMode('text');
  }, [voiceChat, sendMessage]);

  // Handle suggested action tap
  const handleSuggestedAction = useCallback(
    (action: string, targetId?: string) => {
      dismissSuggestedAction();

      switch (action) {
        case 'clear_batch':
          navigation.navigate('BatchGate');
          break;
        case 'view_card':
          if (targetId) {
            navigation.navigate('Chat', {
              conversationId: conversationId ?? undefined,
              linkedCardId: targetId,
            });
          }
          break;
        case 'schedule':
          // Open calendar/scheduling screen (future)
          sendMessage('Check my calendar for available slots');
          break;
        default:
          break;
      }
    },
    [dismissSuggestedAction, navigation, conversationId, sendMessage]
  );

  // Handle citation press
  const handlePressCitation = useCallback((citation: ChunkCitation) => {
    // Navigate to source viewer with citation context
    // This would open the email source at the specific chunk
    console.log('[ChatScreen] Citation pressed:', citation.chunk_id);
  }, []);

  // Handle audio playback
  const handlePlayAudio = useCallback(
    (url: string) => {
      if (currentlyPlayingAudio === url) {
        voiceChat.stopPlayback();
        setCurrentlyPlayingAudio(null);
      } else {
        voiceChat.playTTS(url);
        setCurrentlyPlayingAudio(url);
      }
    },
    [currentlyPlayingAudio, voiceChat]
  );

  // Navigate to full voice mode screen
  const handleToggleVoiceMode = useCallback(() => {
    if (conversationId) {
      navigation.navigate('ChatVoice', { conversationId });
    }
  }, [navigation, conversationId]);

  // Dynamic styles based on theme
  const dynamicStyles = StyleSheet.create({
    container: {
      flex: 1,
      backgroundColor: colors.background,
    },
    header: {
      flexDirection: 'row',
      alignItems: 'center',
      paddingHorizontal: spacing[4],
      paddingVertical: spacing[3],
      backgroundColor: colors.surface,
      borderBottomWidth: 1,
      borderBottomColor: colors.border,
    },
    backButton: {
      width: spacing[8],
      height: spacing[8],
      justifyContent: 'center',
      alignItems: 'center',
      marginRight: spacing[1],
    },
    backIcon: {
      fontSize: fontSize['2xl'],
      color: colors.textSecondary,
      fontWeight: fontWeight.light,
    },
    headerCenter: {
      flex: 1,
      alignItems: 'center',
    },
    headerTitle: {
      fontSize: fontSize.base,
      fontWeight: fontWeight.semibold,
      color: colors.textPrimary,
    },
    headerSubtitle: {
      fontSize: fontSize.xs,
      color: colors.textTertiary,
      marginTop: 1,
    },
    voiceToggleButton: {
      width: spacing[8],
      height: spacing[8],
      justifyContent: 'center',
      alignItems: 'center',
      marginLeft: spacing[1],
    },
    voiceToggleIcon: {
      fontSize: fontSize.lg,
    },
    messageArea: {
      flex: 1,
    },
    transcriptionContainer: {
      paddingHorizontal: spacing[4],
      paddingVertical: spacing[2],
      backgroundColor: colors.background,
    },
  });

  return (
    <SafeAreaView style={dynamicStyles.container} edges={['top']}>
      {/* ── Header ────────────────────────────────────────── */}
      <View style={dynamicStyles.header}>
        <TouchableOpacity
          onPress={() => navigation.goBack()}
          style={dynamicStyles.backButton}
          accessibilityLabel="Go back"
          accessibilityRole="button"
        >
          <Text style={dynamicStyles.backIcon}>‹</Text>
        </TouchableOpacity>

        <View style={dynamicStyles.headerCenter}>
          <Text style={dynamicStyles.headerTitle} numberOfLines={1}>
            {conversationTitle}
          </Text>
          <Text style={dynamicStyles.headerSubtitle}>
            {isLoading ? 'Thinking…' : `${messages.length} messages`}
          </Text>
        </View>

        <ThemeToggle size="sm" />

        <TouchableOpacity
          onPress={handleToggleVoiceMode}
          style={dynamicStyles.voiceToggleButton}
          accessibilityLabel="Open full voice mode"
          accessibilityRole="button"
        >
          <Text style={dynamicStyles.voiceToggleIcon}>🎙</Text>
        </TouchableOpacity>
      </View>

      {/* ── Message List ──────────────────────────────────── */}
      <View style={dynamicStyles.messageArea}>
        <MessageList
          messages={messages}
          isLoading={isLoading}
          onPressCitation={handlePressCitation}
          onPlayAudio={handlePlayAudio}
          currentlyPlayingAudioUrl={currentlyPlayingAudio}
        />
      </View>

      {/* ── Voice Mode: Transcription View ────────────────── */}
      {inputMode === 'voice' && voiceChat.isRecording && (
        <View style={dynamicStyles.transcriptionContainer}>
          <TranscriptionView
            text={voiceChat.transcription}
            isFinal={!voiceChat.isRecording}
            isActive={voiceChat.isRecording}
          />
        </View>
      )}

      {/* ── Suggested Actions ─────────────────────────────── */}
      {suggestedAction && suggestedAction !== 'none' && (
        <SuggestedAction
          action={suggestedAction}
          onPress={handleSuggestedAction}
          targetId={actionTargetId}
        />
      )}

      {/* ── Input Bar ─────────────────────────────────────── */}
      <KeyboardAvoidingView
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
        keyboardVerticalOffset={Platform.OS === 'ios' ? spacing[2] : 0}
      >
        <ChatInput
          value={inputText}
          onChangeText={setInputText}
          onSend={handleSend}
          isLoading={isLoading}
          mode={inputMode}
          onModeChange={setInputMode}
          isRecording={voiceChat.isRecording}
          onStartRecording={voiceChat.startRecording}
          onStopRecording={handleStopRecording}
          waveformAmplitude={voiceChat.amplitude}
        />
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
};
