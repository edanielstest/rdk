// Package arm defines the arm that a robot uses to manipulate objects.
package arm

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/edaniels/golog"
	commonpb "go.viam.com/api/common/v1"
	pb "go.viam.com/api/component/arm/v1"
	viamutils "go.viam.com/utils"
	"go.viam.com/utils/rpc"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/config"
	"go.viam.com/rdk/data"
	"go.viam.com/rdk/motionplan"
	"go.viam.com/rdk/referenceframe"
	"go.viam.com/rdk/registry"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/robot"
	"go.viam.com/rdk/robot/framesystem"
	"go.viam.com/rdk/spatialmath"
	"go.viam.com/rdk/subtype"
	"go.viam.com/rdk/utils"
)

func init() {
	registry.RegisterResourceSubtype(Subtype, registry.ResourceSubtype{
		Reconfigurable: WrapWithReconfigurable,
		Status: func(ctx context.Context, resource interface{}) (interface{}, error) {
			return CreateStatus(ctx, resource)
		},
		RegisterRPCService: func(ctx context.Context, rpcServer rpc.Server, subtypeSvc subtype.Service) error {
			return rpcServer.RegisterServiceServer(
				ctx,
				&pb.ArmService_ServiceDesc,
				NewServer(subtypeSvc),
				pb.RegisterArmServiceHandlerFromEndpoint,
			)
		},
		RPCServiceDesc: &pb.ArmService_ServiceDesc,
		RPCClient: func(ctx context.Context, conn rpc.ClientConn, name string, logger golog.Logger) interface{} {
			return NewClientFromConn(ctx, conn, name, logger)
		},
	})

	data.RegisterCollector(data.MethodMetadata{
		Subtype:    SubtypeName,
		MethodName: endPosition.String(),
	}, newEndPositionCollector)
	data.RegisterCollector(data.MethodMetadata{
		Subtype:    SubtypeName,
		MethodName: jointPositions.String(),
	}, newJointPositionsCollector)
}

// SubtypeName is a constant that identifies the component resource subtype string "arm".
const (
	SubtypeName = resource.SubtypeName("arm")
)

var defaultArmPlannerOptions = map[string]interface{}{
	"motion_profile": motionplan.LinearMotionProfile,
}

// Subtype is a constant that identifies the component resource subtype.
var Subtype = resource.NewSubtype(
	resource.ResourceNamespaceRDK,
	resource.ResourceTypeComponent,
	SubtypeName,
)

// Named is a helper for getting the named Arm's typed resource name.
func Named(name string) resource.Name {
	return resource.NameFromSubtype(Subtype, name)
}

// An Arm represents a physical robotic arm that exists in three-dimensional space.
type Arm interface {
	// EndPosition returns the current position of the arm.
	EndPosition(ctx context.Context, extra map[string]interface{}) (*commonpb.Pose, error)

	// MoveToPosition moves the arm to the given absolute position.
	// The worldState argument should be treated as optional by all implementing drivers
	// This will block until done or a new operation cancels this one
	MoveToPosition(ctx context.Context, pose *commonpb.Pose, worldState *commonpb.WorldState, extra map[string]interface{}) error

	// MoveToJointPositions moves the arm's joints to the given positions.
	// This will block until done or a new operation cancels this one
	MoveToJointPositions(ctx context.Context, positionDegs *pb.JointPositions, extra map[string]interface{}) error

	// JointPositions returns the current joint positions of the arm.
	JointPositions(ctx context.Context, extra map[string]interface{}) (*pb.JointPositions, error)

	// Stop stops the arm. It is assumed the arm stops immediately.
	Stop(ctx context.Context, extra map[string]interface{}) error

	generic.Generic
	referenceframe.ModelFramer
	referenceframe.InputEnabled
}

// A LocalArm represents an Arm that can report whether it is moving or not.
type LocalArm interface {
	Arm

	resource.MovingCheckable
}

var (
	_ = Arm(&reconfigurableArm{})
	_ = LocalArm(&reconfigurableLocalArm{})
	_ = resource.Reconfigurable(&reconfigurableArm{})
	_ = resource.Reconfigurable(&reconfigurableLocalArm{})
	_ = viamutils.ContextCloser(&reconfigurableLocalArm{})

	// ErrStopUnimplemented is used for when Stop() is unimplemented.
	ErrStopUnimplemented = errors.New("Stop() unimplemented")
)

// NewUnimplementedInterfaceError is used when there is a failed interface check.
func NewUnimplementedInterfaceError(actual interface{}) error {
	return utils.NewUnimplementedInterfaceError((Arm)(nil), actual)
}

// NewUnimplementedLocalInterfaceError is used when there is a failed interface check.
func NewUnimplementedLocalInterfaceError(actual interface{}) error {
	return utils.NewUnimplementedInterfaceError((LocalArm)(nil), actual)
}

// DependencyTypeError is used when a resource doesn't implement the expected interface.
func DependencyTypeError(name, actual interface{}) error {
	return utils.DependencyTypeError(name, (Arm)(nil), actual)
}

// FromDependencies is a helper for getting the named arm from a collection of
// dependencies.
func FromDependencies(deps registry.Dependencies, name string) (Arm, error) {
	res, ok := deps[Named(name)]
	if !ok {
		return nil, utils.DependencyNotFoundError(name)
	}
	part, ok := res.(Arm)
	if !ok {
		return nil, DependencyTypeError(name, res)
	}
	return part, nil
}

// FromRobot is a helper for getting the named Arm from the given Robot.
func FromRobot(r robot.Robot, name string) (Arm, error) {
	res, err := r.ResourceByName(Named(name))
	if err != nil {
		return nil, err
	}
	part, ok := res.(Arm)
	if !ok {
		return nil, NewUnimplementedInterfaceError(res)
	}
	return part, nil
}

// NamesFromRobot is a helper for getting all arm names from the given Robot.
func NamesFromRobot(r robot.Robot) []string {
	return robot.NamesBySubtype(r, Subtype)
}

// CreateStatus creates a status from the arm.
func CreateStatus(ctx context.Context, resource interface{}) (*pb.Status, error) {
	arm, ok := resource.(LocalArm)
	if !ok {
		return nil, NewUnimplementedLocalInterfaceError(resource)
	}
	endPosition, err := arm.EndPosition(ctx, nil)
	if err != nil {
		return nil, err
	}
	jointPositions, err := arm.JointPositions(ctx, nil)
	if err != nil {
		return nil, err
	}
	isMoving, err := arm.IsMoving(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.Status{EndPosition: endPosition, JointPositions: jointPositions, IsMoving: isMoving}, nil
}

type reconfigurableArm struct {
	mu     sync.RWMutex
	actual Arm
}

func (r *reconfigurableArm) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.DoCommand(ctx, cmd)
}

func (r *reconfigurableArm) ProxyFor() interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual
}

func (r *reconfigurableArm) EndPosition(ctx context.Context, extra map[string]interface{}) (*commonpb.Pose, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.EndPosition(ctx, extra)
}

func (r *reconfigurableArm) MoveToPosition(
	ctx context.Context,
	pose *commonpb.Pose,
	worldState *commonpb.WorldState,
	extra map[string]interface{},
) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.MoveToPosition(ctx, pose, worldState, extra)
}

func (r *reconfigurableArm) MoveToJointPositions(ctx context.Context, positionDegs *pb.JointPositions, extra map[string]interface{}) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.MoveToJointPositions(ctx, positionDegs, extra)
}

func (r *reconfigurableArm) JointPositions(ctx context.Context, extra map[string]interface{}) (*pb.JointPositions, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.JointPositions(ctx, extra)
}

func (r *reconfigurableArm) Stop(ctx context.Context, extra map[string]interface{}) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.Stop(ctx, extra)
}

func (r *reconfigurableArm) ModelFrame() referenceframe.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.ModelFrame()
}

func (r *reconfigurableArm) CurrentInputs(ctx context.Context) ([]referenceframe.Input, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.CurrentInputs(ctx)
}

func (r *reconfigurableArm) GoToInputs(ctx context.Context, goal []referenceframe.Input) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.GoToInputs(ctx, goal)
}

func (r *reconfigurableArm) Close(ctx context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return viamutils.TryClose(ctx, r.actual)
}

func (r *reconfigurableArm) Reconfigure(ctx context.Context, newArm resource.Reconfigurable) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reconfigure(ctx, newArm)
}

func (r *reconfigurableArm) reconfigure(ctx context.Context, newArm resource.Reconfigurable) error {
	arm, ok := newArm.(*reconfigurableArm)
	if !ok {
		return utils.NewUnexpectedTypeError(r, newArm)
	}
	if err := viamutils.TryClose(ctx, r.actual); err != nil {
		golog.Global().Errorw("error closing old", "error", err)
	}
	r.actual = arm.actual
	return nil
}

// UpdateAction helps hint the reconfiguration process on what strategy to use given a modified config.
// See config.ShouldUpdateAction for more information.
func (r *reconfigurableArm) UpdateAction(c *config.Component) config.UpdateActionType {
	obj, canUpdate := r.actual.(config.ComponentUpdate)
	if canUpdate {
		return obj.UpdateAction(c)
	}
	return config.Reconfigure
}

type reconfigurableLocalArm struct {
	*reconfigurableArm
	actual LocalArm
}

func (r *reconfigurableLocalArm) IsMoving(ctx context.Context) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.actual.IsMoving(ctx)
}

func (r *reconfigurableLocalArm) Reconfigure(ctx context.Context, newArm resource.Reconfigurable) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	arm, ok := newArm.(*reconfigurableLocalArm)
	if !ok {
		return utils.NewUnexpectedTypeError(r, newArm)
	}
	if err := viamutils.TryClose(ctx, r.actual); err != nil {
		golog.Global().Errorw("error closing old", "error", err)
	}

	r.actual = arm.actual
	return r.reconfigurableArm.reconfigure(ctx, arm.reconfigurableArm)
}

// WrapWithReconfigurable converts a regular Arm implementation to a reconfigurableArm
// and a localArm into a reconfigurableLocalArm
// If arm is already a Reconfigurable, then nothing is done.
func WrapWithReconfigurable(r interface{}) (resource.Reconfigurable, error) {
	arm, ok := r.(Arm)
	if !ok {
		return nil, NewUnimplementedInterfaceError(r)
	}

	if reconfigurable, ok := arm.(*reconfigurableArm); ok {
		return reconfigurable, nil
	}

	rArm := &reconfigurableArm{actual: arm}
	localArm, ok := r.(LocalArm)
	if !ok {
		// is an arm but is not a local arm
		return rArm, nil
	}

	if reconfigurableLocal, ok := localArm.(*reconfigurableLocalArm); ok {
		return reconfigurableLocal, nil
	}
	return &reconfigurableLocalArm{actual: localArm, reconfigurableArm: rArm}, nil
}

// NewPositionFromMetersAndOV returns a three-dimensional arm position
// defined by a point in space in meters and an orientation defined as an OrientationVector.
// See robot.proto for a math explanation.
func NewPositionFromMetersAndOV(x, y, z, th, ox, oy, oz float64) *commonpb.Pose {
	return &commonpb.Pose{
		X:     x * 1000,
		Y:     y * 1000,
		Z:     z * 1000,
		OX:    ox,
		OY:    oy,
		OZ:    oz,
		Theta: th,
	}
}

// PositionGridDiff returns the euclidean distance between
// two arm positions in millimeters.
func PositionGridDiff(a, b *commonpb.Pose) float64 {
	diff := utils.Square(a.X-b.X) +
		utils.Square(a.Y-b.Y) +
		utils.Square(a.Z-b.Z)

	// Pythagorean theorum in 3d uses sqrt, not cube root
	// https://www.mathsisfun.com/geometry/pythagoras-3d.html
	return math.Sqrt(diff)
}

// PositionRotationDiff returns the sum of the squared differences between the angle axis components of two positions.
func PositionRotationDiff(a, b *commonpb.Pose) float64 {
	return utils.Square(a.Theta-b.Theta) +
		utils.Square(a.OX-b.OX) +
		utils.Square(a.OY-b.OY) +
		utils.Square(a.OZ-b.OZ)
}

// Move is a helper function to abstract away movement for general arms.
func Move(ctx context.Context, r robot.Robot, a Arm, dst *commonpb.Pose, worldState *commonpb.WorldState) error {
	solution, err := Plan(ctx, r, a, dst, worldState)
	if err != nil {
		return err
	}
	return GoToWaypoints(ctx, a, solution)
}

// Plan is a helper function to be called by arm implementations to abstract away the default procedure for using the
// motion planning library with arms.
func Plan(
	ctx context.Context,
	r robot.Robot,
	a Arm,
	dst *commonpb.Pose,
	worldState *commonpb.WorldState,
) ([][]referenceframe.Input, error) {
	// build the framesystem
	fs, err := framesystem.RobotFrameSystem(ctx, r, worldState.GetTransforms())
	if err != nil {
		return nil, err
	}
	armName := a.ModelFrame().Name()
	destination := referenceframe.NewPoseInFrame(armName+"_origin", spatialmath.NewPoseFromProtobuf(dst))

	var solutionMap []map[string][]referenceframe.Input

	// PlanRobotMotion needs a frame system which contains the frame being solved for
	if fs.Frame(armName) == nil {
		if worldState != nil {
			if len(worldState.Obstacles) != 0 || len(worldState.InteractionSpaces) != 0 || len(worldState.Transforms) != 0 {
				return nil, errors.New("arm must be in frame system to use worldstate")
			}
		}

		// ephemerally create a framesystem containing just the arm for the solve
		fs = referenceframe.NewEmptySimpleFrameSystem("")
		err := fs.AddFrame(a.ModelFrame(), fs.World())
		if err != nil {
			return nil, err
		}
		destination = referenceframe.NewPoseInFrame(referenceframe.World, spatialmath.NewPoseFromProtobuf(dst))
	}

	solutionMap, err = motionplan.PlanRobotMotion(ctx, destination, a.ModelFrame(), r, fs, worldState, defaultArmPlannerOptions)
	if err != nil {
		return nil, err
	}
	solution := make([][]referenceframe.Input, 0, len(solutionMap))
	for _, step := range solutionMap {
		solution = append(solution, step[armName])
	}
	return solution, nil
}

// GoToWaypoints will visit in turn each of the joint position waypoints generated by a motion planner.
func GoToWaypoints(ctx context.Context, a Arm, waypoints [][]referenceframe.Input) error {
	for _, waypoint := range waypoints {
		err := ctx.Err() // make sure we haven't been cancelled
		if err != nil {
			return err
		}

		err = a.GoToInputs(ctx, waypoint)
		if err != nil {
			return err
		}
	}
	return nil
}
