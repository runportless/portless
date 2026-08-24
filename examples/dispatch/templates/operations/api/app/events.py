"""NATS delivery-event publisher."""

import json
from datetime import UTC, datetime
from typing import Any
from uuid import uuid4

import nats
from nats.aio.client import Client as NATS


class EventPublisher:
    """Publishes versioned delivery events to the managed NATS service."""

    def __init__(self, url: str):
        self.url = url
        self.client: NATS | None = None

    async def connect(self) -> None:
        self.client = await nats.connect(
            servers=[self.url],
            connect_timeout=5,
            reconnect_time_wait=1,
            max_reconnect_attempts=10,
        )

    async def close(self) -> None:
        if self.client is not None and not self.client.is_closed:
            await self.client.drain()

    async def publish(
        self,
        event_type: str,
        delivery: dict[str, Any],
        trace_id: str | None,
    ) -> dict[str, Any]:
        if self.client is None:
            raise RuntimeError("NATS publisher is not connected")
        event = {
            "schemaVersion": 1,
            "eventId": f"evt_{uuid4().hex[:12]}",
            "type": event_type,
            "deliveryId": delivery["id"],
            "status": delivery["status"],
            "occurredAt": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
            **({"traceId": trace_id} if trace_id else {}),
        }
        subject = "dispatch." + event_type
        await self.client.publish(subject, json.dumps(event).encode())
        await self.client.flush(timeout=2)
        return event

