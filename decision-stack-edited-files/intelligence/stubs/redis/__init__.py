from asyncio import Lock

class Redis:
    def __init__(self, *args, **kwargs):
        pass
    async def get(self, *args, **kwargs):
        return None
    async def set(self, *args, **kwargs):
        pass
    async def delete(self, *args, **kwargs):
        pass
    async def close(self):
        pass

def from_url(*args, **kwargs):
    return Redis()
