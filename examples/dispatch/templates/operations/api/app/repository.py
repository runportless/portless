"""MySQL persistence for deliveries."""

from datetime import datetime
from typing import Any
from urllib.parse import unquote, urlparse

import aiomysql

from .domain import DomainError, next_status


class DeliveryRepository:
    """Owns the example's small MySQL schema and transactions."""

    def __init__(self, url: str):
        self.url = url
        self.pool: aiomysql.Pool | None = None

    async def connect(self) -> None:
        parsed = urlparse(self.url)
        if parsed.scheme not in {"mysql", "mysql2"} or not parsed.hostname:
            raise RuntimeError("DATABASE_URL must be a MySQL URL")
        self.pool = await aiomysql.create_pool(
            host=parsed.hostname,
            port=parsed.port or 3306,
            user=unquote(parsed.username or ""),
            password=unquote(parsed.password or ""),
            db=parsed.path.lstrip("/"),
            minsize=1,
            maxsize=5,
            autocommit=False,
        )
        async with self.pool.acquire() as connection:
            async with connection.cursor() as cursor:
                await cursor.execute(
                    """
                    CREATE TABLE IF NOT EXISTS deliveries (
                        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
                        pickup_code VARCHAR(80) NOT NULL,
                        destination_code VARCHAR(80) NOT NULL,
                        parcel_size VARCHAR(16) NOT NULL,
                        priority VARCHAR(16) NOT NULL,
                        distance_meters INT UNSIGNED NOT NULL,
                        eta_minutes INT UNSIGNED NOT NULL,
                        price_cents INT UNSIGNED NOT NULL,
                        route_strategy VARCHAR(40) NOT NULL,
                        status VARCHAR(24) NOT NULL,
                        created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
                        updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
                            ON UPDATE CURRENT_TIMESTAMP(6)
                    )
                    """
                )
            await connection.commit()

    def close(self) -> None:
        if self.pool is not None:
            self.pool.close()

    async def wait_closed(self) -> None:
        if self.pool is not None:
            await self.pool.wait_closed()

    async def list(self) -> list[dict[str, Any]]:
        pool = self._pool()
        async with pool.acquire() as connection:
            async with connection.cursor(aiomysql.DictCursor) as cursor:
                await cursor.execute("SELECT * FROM deliveries ORDER BY id DESC LIMIT 100")
                rows = await cursor.fetchall()
        return [delivery_from_row(row) for row in rows]

    async def get(self, external_id: str) -> dict[str, Any]:
        row = await self._select(external_id)
        if row is None:
            raise DomainError("DELIVERY_NOT_FOUND", f"No delivery has ID {external_id}", 404)
        return delivery_from_row(row)

    async def create(
        self, request: dict[str, Any], estimate: dict[str, Any]
    ) -> dict[str, Any]:
        pool = self._pool()
        async with pool.acquire() as connection:
            try:
                async with connection.cursor() as cursor:
                    await cursor.execute(
                        """
                        INSERT INTO deliveries (
                            pickup_code, destination_code, parcel_size, priority,
                            distance_meters, eta_minutes, price_cents, route_strategy, status
                        ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, 'scheduled')
                        """,
                        (
                            request["pickup"],
                            request["destination"],
                            request["parcel_size"],
                            request["priority"],
                            round(float(estimate["distanceKm"]) * 1000),
                            int(estimate["etaMinutes"]),
                            int(estimate["priceCents"]),
                            estimate["strategy"],
                        ),
                    )
                    identifier = cursor.lastrowid
                await connection.commit()
            except Exception:
                await connection.rollback()
                raise
        return await self.get(format_id(identifier))

    async def advance(self, external_id: str) -> dict[str, Any]:
        identifier = parse_id(external_id)
        pool = self._pool()
        async with pool.acquire() as connection:
            try:
                await connection.begin()
                async with connection.cursor(aiomysql.DictCursor) as cursor:
                    await cursor.execute(
                        "SELECT * FROM deliveries WHERE id = %s FOR UPDATE", (identifier,)
                    )
                    row = await cursor.fetchone()
                    if row is None:
                        raise DomainError(
                            "DELIVERY_NOT_FOUND", f"No delivery has ID {external_id}", 404
                        )
                    status = next_status(row["status"])
                    await cursor.execute(
                        "UPDATE deliveries SET status = %s WHERE id = %s",
                        (status, identifier),
                    )
                    await cursor.execute("SELECT * FROM deliveries WHERE id = %s", (identifier,))
                    updated = await cursor.fetchone()
                await connection.commit()
            except Exception:
                await connection.rollback()
                raise
        return delivery_from_row(updated)

    async def _select(self, external_id: str) -> dict[str, Any] | None:
        identifier = parse_id(external_id)
        pool = self._pool()
        async with pool.acquire() as connection:
            async with connection.cursor(aiomysql.DictCursor) as cursor:
                await cursor.execute("SELECT * FROM deliveries WHERE id = %s", (identifier,))
                return await cursor.fetchone()

    def _pool(self) -> aiomysql.Pool:
        if self.pool is None:
            raise RuntimeError("delivery repository is not connected")
        return self.pool


def parse_id(value: str) -> int:
    """Convert the public D-0001 identity to its private database key."""

    if not value.startswith("D-") or not value[2:].isdigit():
        raise DomainError("DELIVERY_NOT_FOUND", f"No delivery has ID {value}", 404)
    return int(value[2:])


def format_id(value: int) -> str:
    """Format a private database key as a readable delivery identity."""

    return f"D-{value:04d}"


def delivery_from_row(row: dict[str, Any]) -> dict[str, Any]:
    """Map one MySQL row to the public response shape."""

    return {
        "id": format_id(row["id"]),
        "pickup": row["pickup_code"],
        "destination": row["destination_code"],
        "parcelSize": row["parcel_size"],
        "priority": row["priority"],
        "distanceKm": row["distance_meters"] / 1000,
        "etaMinutes": row["eta_minutes"],
        "priceCents": row["price_cents"],
        "routeStrategy": row["route_strategy"],
        "status": row["status"],
        "createdAt": isoformat(row["created_at"]),
        "updatedAt": isoformat(row["updated_at"]),
    }


def isoformat(value: datetime | str) -> str:
    """Return an RFC 3339 timestamp from a MySQL datetime."""

    if isinstance(value, datetime):
        return value.isoformat(timespec="milliseconds") + "Z"
    return str(value)

