import React from 'react';
import {Composition} from 'remotion';
import {PortlessExplainer} from './PortlessExplainer';

export const VideoRoot: React.FC = () => (
  <Composition
    id="PortlessExplainer"
    component={PortlessExplainer}
    durationInFrames={1395}
    fps={30}
    width={1280}
    height={720}
  />
);
