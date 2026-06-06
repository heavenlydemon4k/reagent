class Distance:
    COSINE = "Cosine"

class VectorParams:
    def __init__(self, size, distance):
        self.size = size
        self.distance = distance

class PointStruct:
    def __init__(self, id, vector, payload):
        self.id = id
        self.vector = vector
        self.payload = payload


class FieldCondition:
    def __init__(self, key=None, match=None):
        self.key = key
        self.match = match


class Filter:
    def __init__(self, must=None):
        self.must = must or []


class MatchValue:
    def __init__(self, value=None):
        self.value = value
