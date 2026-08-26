package dev.portless.store.inventory;

public record InventoryReservationOutcome(
        Status status,
        InventoryReservationResult result,
        InventoryAvailability availability) {
    public enum Status {
        RESERVED,
        INSUFFICIENT,
        NOT_FOUND
    }

    public static InventoryReservationOutcome reserved(InventoryReservationResult result) {
        return new InventoryReservationOutcome(Status.RESERVED, result, null);
    }

    public static InventoryReservationOutcome insufficient(InventoryAvailability availability) {
        return new InventoryReservationOutcome(Status.INSUFFICIENT, null, availability);
    }

    public static InventoryReservationOutcome notFound() {
        return new InventoryReservationOutcome(Status.NOT_FOUND, null, null);
    }
}
