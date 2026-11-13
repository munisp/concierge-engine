"""Temporal worker entrypoint for the payout saga.

    CE_TEMPORAL_ADDRESS=localhost:7233 python -m temporal_worker.worker

Task queue: "payouts". The core-api payout create endpoint (or an outbox
poller) starts PayoutSagaWorkflow with workflow_id = payout:{payout_id} —
idempotent start is Temporal's WorkflowIDReusePolicy, so duplicate triggers
are absorbed.
"""

from __future__ import annotations

import asyncio
import os

from temporalio.client import Client
from temporalio.worker import Worker

from .activities import (compensate_payout, execute_payout, get_payout,
                         quote_payout)
from .workflows import PayoutSagaWorkflow

TASK_QUEUE = "payouts"


async def main() -> None:
    address = os.getenv("CE_TEMPORAL_ADDRESS", "localhost:7233")
    namespace = os.getenv("CE_TEMPORAL_NAMESPACE", "default")
    client = await Client.connect(address, namespace=namespace)
    worker = Worker(
        client,
        task_queue=TASK_QUEUE,
        workflows=[PayoutSagaWorkflow],
        activities=[quote_payout, execute_payout, compensate_payout, get_payout],
    )
    print(f"temporal worker listening on {address} task_queue={TASK_QUEUE}")
    await worker.run()


if __name__ == "__main__":
    asyncio.run(main())
