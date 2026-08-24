"""HTTP request and response models."""

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class CamelModel(BaseModel):
    """A model that serializes field aliases used by the browser app."""

    model_config = ConfigDict(populate_by_name=True)


class DeliveryCreate(CamelModel):
    """Fields accepted when scheduling a delivery."""

    pickup: str = Field(min_length=1, max_length=80)
    destination: str = Field(min_length=1, max_length=80)
    parcel_size: Literal["small", "medium", "large"] = Field(alias="parcelSize")
    priority: Literal["standard", "express"]


class Estimate(CamelModel):
    """A deterministic route estimate."""

    pickup: str
    destination: str
    distance_km: float = Field(alias="distanceKm")
    eta_minutes: int = Field(alias="etaMinutes")
    price_cents: int = Field(alias="priceCents")
    strategy: str


class Delivery(CamelModel):
    """A persisted delivery returned to a client."""

    id: str
    pickup: str
    destination: str
    parcel_size: str = Field(alias="parcelSize")
    priority: str
    distance_km: float = Field(alias="distanceKm")
    eta_minutes: int = Field(alias="etaMinutes")
    price_cents: int = Field(alias="priceCents")
    route_strategy: str = Field(alias="routeStrategy")
    status: str
    created_at: datetime = Field(alias="createdAt")
    updated_at: datetime = Field(alias="updatedAt")

