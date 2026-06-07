// Decision Stack — Full-Screen Voice Mode for Chat
// Immersive voice interface with large waveform, transcription, and TTS playback

import React, { useCallback, useEffect, useState } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  StatusBar,
} from 'react-native';
import { useNavigation, useRoute, type RouteProp } from '@react-navigation/native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { palette } from '@theme/colors';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import { useChat } from '@hooks/useChat';
import { useVoiceChat } from '@hooks/useVoiceChat';
import { VoiceWaveform } from '@components/voice/VoiceWaveform';
import { TranscriptionView } from '@components/voice/TranscriptionView';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import type { RootStackParamList } from '@navigation/AppNavigator';

type ChatVoiceRouteProp = RouteProp<RootStackParamList, 'ChatVoice'>;
type ChatVoiceNavigationProp = NativeStackNavigationProp<
  RootStackParamList,
  'ChatVoice'
>;

/**
 * ChatVoiceScreen — immersive full-screen voice interface.
 *
 * Flow:
 * 1. Screen opens → shows intro / ready state
 * 2. User taps mic or holds to record → waveform animates
 * 3. Live transcription appears as user speaks
 * 4. User stops → message sent, processing indicator shown
 * 5. Assistant response arrives → auto-play TTS audio
 * 6. Waveform shows playback visualization
 * 7. User can reply or dismiss
 *
 * Visual: Darkened background with large central waveform.
 */
export const ChatVoiceScreen: React.FC = () => {
  const navigation = useNavigation<ChatVoiceNavigationProp>();
  const route = useRoute<ChatVoiceRouteProp>();
  const conversationId = route.params?.conversationId;

  const {
    messages,
    isLoading,
    sendMessage,
    sendVoiceMessage,
    conversationTitle,
  } = useChat(conversationId);

  const voice = useVoiceChat();
  const [phase, setPhase] = useState<
    'ready' | 'listening' | 'processing' | 'responding'
  >('ready');
  const [lastAssistantMessage, setLastAssistantMessage] = useState<string | null>(null);

  // Watch for new assistant messages
  useEffect(() => {
    const lastMsg = messages[messages.length - 1];
    if (lastMsg?.role === 'assistant') {
      setLastAssistantMessage(lastMsg.content);
      setPhase('responding');

      // Auto-play TTS if available
      if (lastMsg.audio_url) {
        voice.playTTS(lastMsg.audio_url);
      }
    }
  }, [messages, voice]);

  // Start recording
  const handleStartRecording = useCallback(async () => {
    setPhase('listening');
    voice.reset();
    await voice.startRecording();
  }, [voice]);

  // Stop recording and send
  const handleStopRecording = useCallback(async () => {
    setPhase('processing');
    const transcription = await voice.stopRecording();

    if (transcription && transcription.trim()) {
      await sendMessage(transcription.trim());
    }
  }, [voice, sendMessage]);

  // Toggle recording based on current phase
  const handleMainAction = useCallback(() => {
    switch (phase) {
      case 'ready':
      case 'responding':
        handleStartRecording();
        break;
      case 'listening':
        handleStopRecording();
        break;
      case 'processing':
        // Wait — don't allow interrupt
        break;
    }
  }, [phase, handleStartRecording, handleStopRecording]);

  // Dismiss voice screen
  const handleDismiss = useCallback(() => {
    voice.reset();
    navigation.goBack();
  }, [voice, navigation]);

  // Switch to text chat
  const handleSwitchToText = useCallback(() => {
    voice.reset();
    if (conversationId) {
      navigation.navigate('Chat', { conversationId });
    } else {
      navigation.goBack();
    }
  }, [voice, navigation, conversationId]);

  // Determine waveform color based on phase
  const waveformColor =
    phase === 'listening'
      ? palette.sand[400]
      : phase === 'processing'
      ? palette.steel[400]
      : phase === 'responding'
      ? palette.sage[500]
      : palette.ink[300];

  const isRecording = phase === 'listening';
  const isProcessing = phase === 'processing';

  return (
    <SafeAreaView style={styles.container} edges={['top']}>
      <StatusBar barStyle="light-content" />

      {/* Top bar */}
      <View style={styles.topBar}>
        <TouchableOpacity
          onPress={handleDismiss}
          style={styles.closeButton}
          accessibilityLabel="Close voice mode"
          accessibilityRole="button"
        >
          <Text style={styles.closeIcon}>✕</Text>
        </TouchableOpacity>

        <Text style={styles.topBarTitle} numberOfLines={1}>
          {conversationTitle}
        </Text>

        <TouchableOpacity
          onPress={handleSwitchToText}
          style={styles.textModeButton}
          accessibilityLabel="Switch to text mode"
          accessibilityRole="button"
        >
          <Text style={styles.textModeIcon}>⌨</Text>
        </TouchableOpacity>
      </View>

      {/* Main content area */}
      <View style={styles.content}>
        {/* Status label */}
        <Text style={styles.statusLabel}>
          {isRecording
            ? 'Listening…'
            : isProcessing
            ? 'Processing…'
            : phase === 'responding'
            ? 'Responding'
            : 'Tap to speak'}
        </Text>

        {/* Large waveform visualization */}
        <View style={styles.waveformContainer}>
          <VoiceWaveform
            amplitude={voice.amplitude}
            isActive={isRecording}
            color={waveformColor}
          />
        </View>

        {/* Transcription display */}
        <View style={styles.transcriptionContainer}>
          <TranscriptionView
            text={voice.transcription}
            isFinal={!isRecording && voice.transcription.length > 0}
            isActive={isRecording}
          />
        </View>

        {/* Last assistant response preview */}
        {lastAssistantMessage && phase === 'responding' && (
          <View style={styles.responsePreview}>
            <Text style={styles.responsePreviewLabel}>Assistant</Text>
            <Text style={styles.responsePreviewText} numberOfLines={3}>
              {lastAssistantMessage}
            </Text>
          </View>
        )}
      </View>

      {/* Bottom action area */}
      <View style={styles.bottomArea}>
        {/* Large tap target for recording */}
        <TouchableOpacity
          onPress={handleMainAction}
          style={[
            styles.mainButton,
            isRecording && styles.mainButtonRecording,
            isProcessing && styles.mainButtonProcessing,
          ]}
          disabled={isProcessing}
          activeOpacity={0.8}
          accessibilityLabel={
            isRecording ? 'Stop recording' : 'Start recording'
          }
          accessibilityRole="button"
        >
          <View style={styles.mainButtonInner}>
            {isRecording ? (
              <View style={styles.stopSquare} />
            ) : isProcessing ? (
              <Text style={styles.processingDots}>···</Text>
            ) : (
              <View style={styles.micIconContainer}>
                <View style={styles.micHead} />
                <View style={styles.micStem} />
                <View style={styles.micBase} />
              </View>
            )}
          </View>
        </TouchableOpacity>

        <Text style={styles.hintText}>
          {isRecording
            ? 'Tap to stop'
            : isProcessing
            ? 'One moment…'
            : 'Tap microphone to start'}
        </Text>
      </View>
    </SafeAreaView>
  );
};

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: palette.ink[900],
  },
  topBar: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[3],
  },
  closeButton: {
    width: spacing[8],
    height: spacing[8],
    justifyContent: 'center',
    alignItems: 'center',
  },
  closeIcon: {
    fontSize: fontSize.lg,
    color: palette.ink[300],
    fontWeight: fontWeight.semibold,
  },
  topBarTitle: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
    color: palette.ink[400],
    flex: 1,
    textAlign: 'center',
    marginHorizontal: spacing[4],
  },
  textModeButton: {
    width: spacing[8],
    height: spacing[8],
    justifyContent: 'center',
    alignItems: 'center',
  },
  textModeIcon: {
    fontSize: fontSize.lg,
    color: palette.ink[300],
  },
  content: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: spacing[6],
  },
  statusLabel: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
    color: palette.ink[400],
    textTransform: 'uppercase',
    letterSpacing: 2,
    marginBottom: spacing[6],
  },
  waveformContainer: {
    width: '100%',
    height: spacing[20],
    justifyContent: 'center',
    alignItems: 'center',
    marginBottom: spacing[6],
  },
  transcriptionContainer: {
    width: '100%',
    marginBottom: spacing[6],
  },
  responsePreview: {
    backgroundColor: palette.ink[800],
    borderRadius: spacing[3],
    padding: spacing[4],
    width: '100%',
    borderLeftWidth: 3,
    borderLeftColor: palette.sage[500],
  },
  responsePreviewLabel: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.semibold,
    color: palette.sage[400],
    textTransform: 'uppercase',
    letterSpacing: 1,
    marginBottom: spacing[1],
  },
  responsePreviewText: {
    fontSize: fontSize.base,
    color: palette.ink[200],
    lineHeight: 22,
  },
  bottomArea: {
    alignItems: 'center',
    paddingBottom: spacing[8],
    paddingHorizontal: spacing[6],
  },
  mainButton: {
    width: spacing[20],
    height: spacing[20],
    borderRadius: spacing[10],
    backgroundColor: palette.sand[400],
    justifyContent: 'center',
    alignItems: 'center',
    marginBottom: spacing[4],
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 4 },
    shadowRadius: 16,
    shadowOpacity: 0.3,
    elevation: 10,
  },
  mainButtonRecording: {
    backgroundColor: palette.rose[500],
    shadowColor: palette.rose[500],
    shadowOpacity: 0.5,
  },
  mainButtonProcessing: {
    backgroundColor: palette.steel[600],
  },
  mainButtonInner: {
    justifyContent: 'center',
    alignItems: 'center',
  },
  stopSquare: {
    width: spacing[5],
    height: spacing[5],
    borderRadius: spacing[1],
    backgroundColor: '#ffffff',
  },
  processingDots: {
    fontSize: fontSize.xl,
    color: '#ffffff',
    fontWeight: fontWeight.bold,
    letterSpacing: 2,
  },
  micIconContainer: {
    alignItems: 'center',
  },
  micHead: {
    width: spacing[5],
    height: spacing[6],
    borderRadius: spacing[2.5],
    backgroundColor: '#ffffff',
    marginBottom: 2,
  },
  micStem: {
    width: 3,
    height: spacing[3],
    backgroundColor: '#ffffff',
  },
  micBase: {
    width: spacing[5],
    height: spacing[1.5],
    borderBottomWidth: 3,
    borderLeftWidth: 3,
    borderRightWidth: 3,
    borderBottomLeftRadius: spacing[2],
    borderBottomRightRadius: spacing[2],
    borderColor: '#ffffff',
    marginTop: -1,
  },
  hintText: {
    fontSize: fontSize.sm,
    color: palette.ink[500],
    fontWeight: fontWeight.medium,
  },
});
