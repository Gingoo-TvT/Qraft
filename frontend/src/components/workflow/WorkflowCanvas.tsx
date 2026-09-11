'use client';

import React, { useMemo, useCallback } from 'react';
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  type Node,
  type Edge,
  type NodeTypes,
  MarkerType,
} from 'reactflow';
import 'reactflow/dist/style.css';

import type { WorkflowState, WorkflowStep } from '@/lib/types';
import { workflowNodeTypes, type StepNodeData } from './NodeTypes';

// ---------------------------------------------------------------------------
// Default step names for the generation workflow
// ---------------------------------------------------------------------------

const DEFAULT_STEP_NAMES = [
  'similarity_check',
  'generate_statement',
  'post_statement_similarity',
  'generate_solution',
  'compile_check',
  'generate_testdata',
  'run_sandbox',
  'validate',
  'assess_feasibility',
  'llm_review',
  'human_review',
  'store',
] as const;

const DEFAULT_STEP_DESCRIPTIONS = [
  'Similar Check',
  'Generate Statement',
  'Statement Dedup',
  'Generate Solution',
  'Compile Check',
  'Generate Test Data',
  'Sandbox Run',
  'Validate',
  'Assess Feasibility',
  'LLM Review',
  'Human Review',
  'Store',
] as const;

// ---------------------------------------------------------------------------
// Edge color by step status
// ---------------------------------------------------------------------------

function edgeColor(status: WorkflowStep['status']): string {
  switch (status) {
    case 'completed':
      return '#10b981'; // success-500
    case 'running':
      return '#2d73ff'; // forge-500
    case 'failed':
      return '#ef4444'; // danger-500
    case 'skipped':
      return '#9fa2a9'; // anvil-300
    default:
      return '#c4c6cb'; // anvil-200
  }
}

function edgeStrokeDasharray(status: WorkflowStep['status']): string | undefined {
  return status === 'skipped' ? '6 3' : undefined;
}

function edgeAnimated(status: WorkflowStep['status']): boolean {
  return status === 'running';
}

// ---------------------------------------------------------------------------
// Layout constants
// ---------------------------------------------------------------------------

const NODE_VERTICAL_GAP = 110;
const NODE_X = 200;
const NODE_Y_START = 40;

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface WorkflowCanvasProps {
  workflowState: WorkflowState;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function WorkflowCanvas({ workflowState }: WorkflowCanvasProps) {
  const steps = useMemo(
    () => workflowState.steps ?? [],
    [workflowState.steps],
  );

  // Build nodes from workflow steps, filling in defaults for the pipeline
  const nodes: Node<StepNodeData>[] = useMemo(() => {
    const stepCount = Math.max(steps.length, DEFAULT_STEP_NAMES.length);
    const result: Node<StepNodeData>[] = [];

    for (let i = 0; i < stepCount; i++) {
      const step: WorkflowStep = steps[i] ?? {
        name: DEFAULT_STEP_NAMES[i] ?? `step_${i + 1}`,
        description: DEFAULT_STEP_DESCRIPTIONS[i] ?? `Step ${i + 1}`,
        status: 'pending',
      };

      result.push({
        id: `step-${i}`,
        type: 'stepNode',
        position: { x: NODE_X, y: NODE_Y_START + i * NODE_VERTICAL_GAP },
        data: { step, stepIndex: i },
        draggable: false,
      });
    }

    return result;
  }, [steps]);

  // Build edges connecting sequential steps
  const edges: Edge[] = useMemo(() => {
    const result: Edge[] = [];

    for (let i = 0; i < nodes.length - 1; i++) {
      const sourceStep: WorkflowStep = steps[i] ?? { name: '', description: '', status: 'pending' };
      const targetStep: WorkflowStep = steps[i + 1] ?? { name: '', description: '', status: 'pending' };

      // Color the edge based on the source step's completion or the target's current state
      const status = sourceStep.status === 'completed' ? 'completed' : targetStep.status;

      result.push({
        id: `edge-${i}-${i + 1}`,
        source: `step-${i}`,
        target: `step-${i + 1}`,
        type: 'smoothstep',
        animated: edgeAnimated(status),
        style: {
          stroke: edgeColor(status),
          strokeWidth: 2,
          strokeDasharray: edgeStrokeDasharray(status),
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: edgeColor(status),
          width: 16,
          height: 16,
        },
      });
    }

    return result;
  }, [nodes, steps]);

  // Minimap node color
  const minimapNodeColor = useCallback(
    (node: Node<StepNodeData>) => {
      const step = node.data?.step;
      if (!step) return '#c4c6cb';
      switch (step.status) {
        case 'completed':
          return '#10b981';
        case 'running':
          return '#2d73ff';
        case 'failed':
          return '#ef4444';
        case 'skipped':
          return '#9fa2a9';
        default:
          return '#c4c6cb';
      }
    },
    [],
  );

  const nodeTypes: NodeTypes = useMemo(() => workflowNodeTypes, []);

  return (
    <div className="h-full w-full min-h-[600px] rounded-lg border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-950">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.3 }}
        nodesDraggable={false}
        nodesConnectable={false}
        panOnScroll
        zoomOnScroll
        minZoom={0.4}
        maxZoom={1.5}
        proOptions={{ hideAttribution: true }}
      >
        <Background color="#e2e3e5" gap={20} size={1} />
        <Controls
          className="!bg-white dark:!bg-anvil-800 !border-anvil-200 dark:!border-anvil-700 !shadow-sm"
          showInteractive={false}
        />
        <MiniMap
          nodeColor={minimapNodeColor}
          maskColor="rgba(0,0,0,0.08)"
          className="!bg-anvil-50 dark:!bg-anvil-900 !border-anvil-200 dark:!border-anvil-700"
          pannable
          zoomable
        />
      </ReactFlow>
    </div>
  );
}
