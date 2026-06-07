import os
import re

print("=== Step 1: Adding chat/profile methods to client API ===")

# Read existing api.ts
api_path = "client/src/services/api.ts"
with open(api_path, "r", encoding="utf-8") as f:
    content = f.read()

# New methods to add
new_methods = '''
  // Chat sessions
  async createChatSession(userId: string, title?: string, context?: object) {
    return this.post('/chat/sessions', { user_id: userId, title, context });
  }

  async listChatSessions(userId: string) {
    return this.get(`/chat/sessions?user_id=${userId}`);
  }

  async getChatSession(sessionId: string) {
    return this.get(`/chat/sessions/${sessionId}`);
  }

  async sendChatMessage(sessionId: string, userId: string, content: string) {
    return this.post(`/chat/sessions/${sessionId}/messages`, { user_id: userId, content });
  }

  async sendChatCard(sessionId: string, cardData: object) {
    return this.post(`/chat/sessions/${sessionId}/cards`, cardData);
  }

  // Profile
  async getProfile(userId: string) {
    return this.get(`/profile/${userId}`);
  }

  async updateProfile(userId: string, updates: object) {
    return this.put(`/profile/${userId}`, updates);
  }

  async getPreferences(userId: string) {
    return this.get(`/profile/${userId}/preferences`);
  }
'''

# Find the last closing brace of the class and insert before it
last_brace = content.rfind("}")
if last_brace != -1:
    new_content = content[:last_brace] + new_methods + "
" + content[last_brace:]
    with open(api_path, "w", encoding="utf-8") as f:
        f.write(new_content)
    print(f"✓ Modified: {api_path}")
else:
    print("✗ Could not find class closing brace. Manual insertion needed.")
    print("Add these methods before the final closing brace of the class:")
    print(new_methods)

print("
=== Step 2: Verification ===")
print("To test the intelligence service:")
print("  cd intelligence")
print("  set PYTHONPATH=C:\\Users\\judas\\Documents\\reagent")
print("  uvicorn intelligence.main:app --reload --port 8000")
print("Then open: http://localhost:8000/docs")

print("
=== Step 3: Git commit and push ===")
print("Run these commands:")
print("  git add client/src/services/api.ts")
print("  git commit -m "feat: add chat and profile client API methods"")
print("  git push origin main")
print("  del add_client_api.py")
